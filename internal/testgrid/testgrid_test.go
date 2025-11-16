package testgrid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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

func startServer(response interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		jsonData, _ := json.Marshal(response)
		w.Write(jsonData) // nolint
	}))
}
