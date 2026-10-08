package testgrid

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/signalhound/api/v1alpha1"
)

const dashboard, tabName = "sig-release-blocking", "kubernetes-ci"

func Test_FetchSummary(t *testing.T) {
	tests := []struct {
		name         string
		dashboard    string
		filterStatus []string
		response     DashboardMapper
		match        bool
	}{
		{
			name:         "successful fetch",
			dashboard:    dashboard,
			filterStatus: []string{v1alpha1.FLAKY_STATUS},
			response: DashboardMapper{
				tabName: {
					OverallState:  v1alpha1.FLAKY_STATUS,
					DashboardName: dashboard,
				},
			},
			match: true,
		},
		{
			name:         "not filtered by wrong state",
			dashboard:    dashboard,
			filterStatus: []string{v1alpha1.FAILING_STATUS},
			response: DashboardMapper{
				tabName: {
					OverallState:  v1alpha1.FLAKY_STATUS,
					DashboardName: dashboard,
				},
			},
		},
		{
			name:         "dashboard not found",
			dashboard:    "nonexistent",
			filterStatus: []string{},
			response:     DashboardMapper{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startServer(tt.response)
			defer server.Close()

			tg := NewTestGrid(server.URL)
			summary, err := tg.FetchTabSummary(tt.dashboard, tt.filterStatus)
			assert.NoError(t, err)

			if tt.match {
				assert.Len(t, summary, len(tt.response))
				for _, dash := range summary {
					assert.Equal(t, dash.DashboardName, dashboard)
					assert.Equal(t, dash.DashboardTab.TabName, tabName)
					assert.Contains(t, dash.DashboardTab.TabURL, tabName)
				}
			}
		})
	}
}

func Test_FetchTable(t *testing.T) {
	tests := []struct {
		name      string
		dashboard string
		tab       string
		response  TestGroup
	}{
		{
			name:      "successful fetch",
			dashboard: "dashboard-test",
			tab:       "tab-test",
			response: TestGroup{
				TestGroupName: "cikubernetese2ecapzmasterwindows",
				Query:         "kubernetes-ci-logs/logs/ci-kubernetes-e2e-capz-master-windows",
				Status:        "Served from cache in 0.16 seconds",
				Timestamps:    []int64{1758999193000},
				Changelists:   []string{"1972011571991285760"},
				Tests: []Test{
					{Name: "ci-kubernetes-build.Overall", ShortTexts: []string{"F"}, Messages: []string{"F"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startServer(tt.response)
			defer server.Close()

			summary := &v1alpha1.DashboardSummary{
				OverallState:  v1alpha1.FLAKY_STATUS,
				DashboardName: dashboard,
				DashboardTab: &v1alpha1.DashboardTab{
					TabName: "cikubernetesbuild",
					TabURL:  server.URL,
				},
			}

			tg := NewTestGrid(server.URL)
			tabTest, err := tg.FetchTabTests(summary, 1, 1)
			assert.NoError(t, err)

			assert.NotEmpty(t, tabTest.StateIcon)
			assert.Equal(t, v1alpha1.FLAKY_STATUS, tabTest.TabState)
			assert.Len(t, tabTest.TestRuns, 1)
			for _, test := range tabTest.TestRuns {
				assert.Contains(t, test.TestName, "Overall")
				assert.Contains(t, test.ErrorMessage, "F")
			}
		})
	}
}

func TestRenderStatuses(t *testing.T) {
	message := "kubetest --timeout triggered"
	tests := []struct {
		name            string
		inputTest       Test
		inputTimestamps []int64
		expectedOutput  string
		expectedCount   int
		expectedIndex   int
	}{
		{
			name: "all short texts must match timestamp",
			inputTest: Test{
				ShortTexts: []string{"", "", "F", "", "F"},
				Messages:   []string{"", "", message, "", message},
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000, 1758952851000, 1758945591000},
			expectedOutput:  formatTestStatus("F", 1758960111000, message) + formatTestStatus("F", 1758945591000, message),
			expectedIndex:   2,
			expectedCount:   2,
		},
		{
			name: "no statuses to render",
			inputTest: Test{
				ShortTexts: []string{"", "", ""},
				Messages:   []string{"", "", ""},
			},
			inputTimestamps: []int64{1620000000, 1620003600, 1620007200},
			expectedOutput:  "",
			expectedCount:   0,
			expectedIndex:   -1,
		},
		{
			name: "short texts skip NO_RESULT columns",
			inputTest: Test{
				ShortTexts: []string{"", "F"},
				Messages:   []string{"", message},
				Statuses:   []Statuses{{Count: 1, Value: 0}, {Count: 1, Value: 1}, {Count: 1, Value: 12}}, // NO_RESULT, PASS, FAIL
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000},
			expectedOutput:  formatTestStatus("F", 1758960111000, message),
			expectedCount:   1,
			expectedIndex:   2,
		},
		{
			name: "short texts skip NO_RESULT columns between and after results",
			inputTest: Test{
				ShortTexts: []string{"", "", "F", "F"},
				Messages:   []string{"", "", message, message},
				Statuses:   []Statuses{{Count: 2, Value: 1}, {Count: 2, Value: 0}, {Count: 2, Value: 12}, {Count: 1, Value: 0}}, // PASS x2, NO_RESULT x2, FAIL x2, NO_RESULT
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000, 1758952851000, 1758945591000, 1758938331000, 1758931071000},
			expectedOutput:  formatTestStatus("F", 1758945591000, message) + formatTestStatus("F", 1758938331000, message),
			expectedCount:   2,
			expectedIndex:   4,
		},
		{
			name: "statuses that miss a column keep the positional mapping",
			inputTest: Test{
				ShortTexts: []string{"", "F"},
				Messages:   []string{"", message},
				Statuses:   []Statuses{{Count: 1, Value: 0}, {Count: 1, Value: 1}, {Count: 1, Value: 12}}, // NO_RESULT, PASS, FAIL
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000, 1758952851000},
			expectedOutput:  formatTestStatus("F", 1758967371000, message),
			expectedCount:   1,
			expectedIndex:   1,
		},
		{
			name: "statuses with fewer results than short texts keep the positional mapping",
			inputTest: Test{
				ShortTexts: []string{"F", "F", "F"},
				Messages:   []string{message, message, message},
				Statuses:   []Statuses{{Count: 1, Value: 0}, {Count: 2, Value: 12}}, // NO_RESULT, FAIL x2
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000},
			expectedOutput: formatTestStatus("F", 1758974631000, message) +
				formatTestStatus("F", 1758967371000, message) +
				formatTestStatus("F", 1758960111000, message),
			expectedCount: 3,
			expectedIndex: 0,
		},
		{
			name: "statuses with a negative count keep the positional mapping",
			inputTest: Test{
				ShortTexts: []string{"", "F"},
				Messages:   []string{"", message},
				Statuses:   []Statuses{{Count: -1, Value: 0}, {Count: 1, Value: 0}, {Count: 1, Value: 1}, {Count: 1, Value: 12}}, // bad count, NO_RESULT, PASS, FAIL
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000},
			expectedOutput:  formatTestStatus("F", 1758967371000, message),
			expectedCount:   1,
			expectedIndex:   1,
		},
		{
			name: "statuses with a huge count keep the positional mapping",
			inputTest: Test{
				ShortTexts: []string{"F"},
				Messages:   []string{message},
				Statuses:   []Statuses{{Count: 1, Value: 0}, {Count: 1, Value: 12}, {Count: math.MaxInt, Value: 0}}, // NO_RESULT, FAIL, NO_RESULT
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000},
			expectedOutput:  formatTestStatus("F", 1758974631000, message),
			expectedCount:   1,
			expectedIndex:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, failureCount, firstFailureIndex := tt.inputTest.RenderStatuses(tt.inputTimestamps)
			assert.Equal(t, tt.expectedOutput, output)
			assert.Equal(t, tt.expectedCount, failureCount)
			assert.Equal(t, tt.expectedIndex, firstFailureIndex)
		})
	}
}

func Test_FetchTableNoResultColumns(t *testing.T) {
	// kubetest.Up row from gce-cos-master-default, trimmed.
	data, err := os.ReadFile("testdata/table_no_result.json")
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()

	summary := &v1alpha1.DashboardSummary{
		OverallState:  v1alpha1.FAILING_STATUS,
		DashboardName: "sig-release-master-blocking",
		DashboardTab:  &v1alpha1.DashboardTab{TabName: "gce-cos-master-default", TabURL: server.URL},
	}
	tab, err := NewTestGrid(server.URL).FetchTabTests(summary, 0, 0)
	require.NoError(t, err)
	require.Len(t, tab.TestRuns, 1)

	// Column 0 has no result, so the first "F" is short_texts[2] but column 3.
	run := tab.TestRuns[0]
	assert.Equal(t, "https://prow.k8s.io/view/gs/kubernetes-ci-logs/logs/ci-kubernetes-e2e-gci-gce/2108180654662356992", run.ProwJobURL)
	assert.Contains(t, run.ErrorMessage,
		formatTestStatus("F", 1791464411000, "error during ./hack/e2e-internal/e2e-up.sh: exit status 2"))
}

func startServer(response interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		jsonData, _ := json.Marshal(response)
		w.Write(jsonData) // nolint
	}))
}
