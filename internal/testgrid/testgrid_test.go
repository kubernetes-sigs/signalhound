package testgrid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/signalhound/api/v1alpha1"
)

const (
	dashboard       = "sig-release-blocking"
	tabName         = "kubernetes-ci"
	testDashboard   = "dashboard-test"
	testTab         = "tab-test"
	testGroupName   = "cikubernetese2ecapzmasterwindows"
	testGroupQuery  = "kubernetes-ci-logs/logs/ci-kubernetes-e2e-capz-master-windows"
	overallTestName = "ci-kubernetes-build.Overall"
	testStatusPass  = 1
	testStatusRun   = 4
)

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

func TestFetchSummaryRejectsNullEntries(t *testing.T) {
	server := startServer(DashboardMapper{tabName: nil})
	defer server.Close()

	_, err := NewTestGrid(server.URL).FetchTabSummary(dashboard, []string{v1alpha1.PASSING_STATUS})

	assert.ErrorContains(t, err, "summary entry")
}

func TestFetchSummaryRejectsNullResponse(t *testing.T) {
	server := startServer(nil)
	defer server.Close()

	_, err := NewTestGrid(server.URL).FetchTabSummary(dashboard, []string{v1alpha1.PASSING_STATUS})

	assert.ErrorContains(t, err, "summary response is null")
}

func Test_FetchTable(t *testing.T) {
	tests := []struct {
		name            string
		dashboard       string
		tab             string
		overallState    string
		response        TestGroup
		minFailure      int
		minFlake        int
		expectedState   string
		expectedRuns    int
		expectedLatest  int64
		expectedFirst   int64
		expectedProwRun string
	}{
		{
			name:            "flaky tab includes failed tests",
			dashboard:       testDashboard,
			tab:             testTab,
			overallState:    v1alpha1.FLAKY_STATUS,
			minFailure:      1,
			minFlake:        1,
			expectedState:   v1alpha1.FLAKY_STATUS,
			expectedRuns:    1,
			expectedLatest:  1758999193000,
			expectedFirst:   1758999193000,
			expectedProwRun: "1972011571991285760",
			response: TestGroup{
				TestGroupName: testGroupName,
				Query:         testGroupQuery,
				Status:        "Served from cache in 0.16 seconds",
				Timestamps:    []int64{1758999193000},
				Changelists:   []string{"1972011571991285760"},
				Tests: []Test{
					{
						Name:       overallTestName,
						ShortTexts: []string{"F"},
						Messages:   []string{"F"},
						Statuses:   []Statuses{{Count: 1, Value: testStatusFail}},
					},
				},
			},
		},
		{
			name:            "passing tab with historical failures is flaky",
			dashboard:       testDashboard,
			tab:             testTab,
			overallState:    v1alpha1.PASSING_STATUS,
			minFailure:      1,
			minFlake:        1,
			expectedState:   v1alpha1.FLAKY_STATUS,
			expectedRuns:    1,
			expectedLatest:  1758999192000,
			expectedFirst:   1758999190000,
			expectedProwRun: "latest-failed-run",
			response: TestGroup{
				TestGroupName: testGroupName,
				Query:         testGroupQuery,
				Timestamps:    []int64{1758999193000, 1758999192000, 1758999191000, 1758999190000},
				Changelists:   []string{"successful-run", "latest-failed-run", "another-successful-run", "first-failed-run"},
				Tests: []Test{
					{
						Name:       overallTestName,
						ShortTexts: []string{"2/2", "1/2", "2/2", "F"},
						Messages:   []string{"2/2 runs passed", "1/2 runs passed", "2/2 runs passed", "F"},
						Statuses: []Statuses{
							{Count: 1, Value: testStatusPass},
							{Count: 1, Value: testStatusFlaky},
							{Count: 1, Value: testStatusPass},
							{Count: 1, Value: testStatusFail},
						},
					},
				},
			},
		},
		{
			name:          "passing tab without historical failures remains passing",
			dashboard:     testDashboard,
			tab:           testTab,
			overallState:  v1alpha1.PASSING_STATUS,
			minFailure:    0,
			minFlake:      0,
			expectedState: v1alpha1.PASSING_STATUS,
			expectedRuns:  0,
			response: TestGroup{
				TestGroupName: testGroupName,
				Query:         testGroupQuery,
				Timestamps:    []int64{1758999193000},
				Changelists:   []string{"1972011571991285760"},
				Tests: []Test{
					{
						Name:       overallTestName,
						ShortTexts: []string{"2/2"},
						Messages:   []string{"2/2 runs passed"},
						Statuses:   []Statuses{{Count: 1, Value: testStatusPass}},
					},
				},
			},
		},
		{
			name:          "passing tab below flake threshold remains passing",
			dashboard:     testDashboard,
			tab:           testTab,
			overallState:  v1alpha1.PASSING_STATUS,
			minFailure:    1,
			minFlake:      2,
			expectedState: v1alpha1.PASSING_STATUS,
			expectedRuns:  0,
			response: TestGroup{
				TestGroupName: testGroupName,
				Query:         testGroupQuery,
				Timestamps:    []int64{1758999193000},
				Changelists:   []string{"1972011571991285760"},
				Tests: []Test{
					{
						Name:       overallTestName,
						ShortTexts: []string{"F"},
						Messages:   []string{"F"},
						Statuses:   []Statuses{{Count: 1, Value: testStatusFail}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startServer(tt.response)
			defer server.Close()

			summary := &v1alpha1.DashboardSummary{
				OverallState:  tt.overallState,
				DashboardName: tt.dashboard,
				DashboardTab: &v1alpha1.DashboardTab{
					TabName: tt.tab,
					TabURL:  server.URL,
				},
			}

			tg := NewTestGrid(server.URL)
			tabTest, err := tg.FetchTabTests(summary, tt.minFailure, tt.minFlake)
			require.NoError(t, err)

			assert.NotEmpty(t, tabTest.StateIcon)
			assert.Equal(t, tt.expectedState, tabTest.TabState)
			assert.Len(t, tabTest.TestRuns, tt.expectedRuns)
			for _, test := range tabTest.TestRuns {
				assert.Contains(t, test.TestName, "Overall")
				assert.Contains(t, test.ErrorMessage, "F")
				assert.Equal(t, tt.expectedLatest, test.LatestTimestamp)
				assert.Equal(t, tt.expectedFirst, test.FirstTimestamp)
				assert.Contains(t, test.ProwJobURL, tt.expectedProwRun)
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
		expectedLatest  int
		expectedFirst   int
	}{
		{
			name: "only failure statuses are rendered",
			inputTest: Test{
				ShortTexts: []string{"2/2", "R", "1/2", "16/16", "F"},
				Messages:   []string{"2/2 runs passed", "Build still running...", message, "16/16 runs passed", message},
				Statuses: []Statuses{
					{Count: 1, Value: testStatusPass},
					{Count: 1, Value: testStatusRun},
					{Count: 1, Value: testStatusFlaky},
					{Count: 1, Value: testStatusPass},
					{Count: 1, Value: testStatusFail},
				},
			},
			inputTimestamps: []int64{1758974631000, 1758967371000, 1758960111000, 1758952851000, 1758945591000},
			expectedOutput:  formatTestStatus("1/2", 1758960111000, message) + formatTestStatus("F", 1758945591000, message),
			expectedLatest:  2,
			expectedFirst:   4,
			expectedCount:   2,
		},
		{
			name: "no statuses to render",
			inputTest: Test{
				ShortTexts: []string{"2/2", "R", "16/16"},
				Messages:   []string{"passed", "running", "passed"},
				Statuses: []Statuses{
					{Count: 1, Value: testStatusPass},
					{Count: 1, Value: testStatusRun},
					{Count: 1, Value: testStatusPass},
				},
			},
			inputTimestamps: []int64{1620000000, 1620003600, 1620007200},
			expectedOutput:  "",
			expectedCount:   0,
			expectedLatest:  -1,
			expectedFirst:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, failureCount, latestFailureIndex, firstFailureIndex, err := tt.inputTest.RenderStatuses(tt.inputTimestamps)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOutput, output)
			assert.Equal(t, tt.expectedCount, failureCount)
			assert.Equal(t, tt.expectedLatest, latestFailureIndex)
			assert.Equal(t, tt.expectedFirst, firstFailureIndex)
		})
	}
}

func TestRenderStatusesRejectsMisalignedColumns(t *testing.T) {
	tests := []struct {
		name       string
		test       Test
		timestamps []int64
		errText    string
	}{
		{
			name: "message count differs from timestamps",
			test: Test{
				ShortTexts: []string{"F", "F"},
				Messages:   []string{"failed"},
				Statuses:   []Statuses{{Count: 2, Value: testStatusFail}},
			},
			timestamps: []int64{1, 2},
			errText:    "column data is not aligned",
		},
		{
			name: "status count differs from timestamps",
			test: Test{
				ShortTexts: []string{"F", "F"},
				Messages:   []string{"failed", "failed"},
				Statuses:   []Statuses{{Count: 1, Value: testStatusFail}},
			},
			timestamps: []int64{1, 2},
			errText:    "status data has 1 columns; expected 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := tt.test.RenderStatuses(tt.timestamps)
			assert.ErrorContains(t, err, tt.errText)
		})
	}
}

func TestFetchTabTestsRejectsMisalignedRows(t *testing.T) {
	server := startServer(TestGroup{
		Query:       testGroupQuery,
		Timestamps:  []int64{1, 2},
		Changelists: []string{"run-1", "run-2"},
		Tests: []Test{
			{
				Name:       overallTestName,
				ShortTexts: []string{"F", "F"},
				Messages:   []string{"failed"},
				Statuses:   []Statuses{{Count: 2, Value: testStatusFail}},
			},
		},
	})
	defer server.Close()

	summary := &v1alpha1.DashboardSummary{
		OverallState:  v1alpha1.PASSING_STATUS,
		DashboardName: testDashboard,
		DashboardTab: &v1alpha1.DashboardTab{
			TabName: testTab,
			TabURL:  server.URL,
		},
	}

	_, err := NewTestGrid(server.URL).FetchTabTests(summary, 1, 1)

	assert.ErrorContains(t, err, "column data is not aligned")
}

func TestFetchTabTestsRejectsNullResponse(t *testing.T) {
	server := startServer(nil)
	defer server.Close()

	summary := &v1alpha1.DashboardSummary{
		OverallState:  v1alpha1.PASSING_STATUS,
		DashboardName: testDashboard,
		DashboardTab: &v1alpha1.DashboardTab{
			TabName: testTab,
			TabURL:  server.URL,
		},
	}

	_, err := NewTestGrid(server.URL).FetchTabTests(summary, 1, 1)

	assert.ErrorContains(t, err, "table response is null")
}

func TestFetchTabTestsRejectsMisalignedChangelists(t *testing.T) {
	server := startServer(TestGroup{
		Query:      testGroupQuery,
		Timestamps: []int64{1},
		Tests: []Test{
			{
				Name:       overallTestName,
				ShortTexts: []string{"F"},
				Messages:   []string{"failed"},
				Statuses:   []Statuses{{Count: 1, Value: testStatusFail}},
			},
		},
	})
	defer server.Close()

	summary := &v1alpha1.DashboardSummary{
		OverallState:  v1alpha1.PASSING_STATUS,
		DashboardName: testDashboard,
		DashboardTab: &v1alpha1.DashboardTab{
			TabName: testTab,
			TabURL:  server.URL,
		},
	}

	_, err := NewTestGrid(server.URL).FetchTabTests(summary, 1, 1)

	assert.ErrorContains(t, err, "1 timestamps, 0 changelists")
}

func startServer(response interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		jsonData, _ := json.Marshal(response)
		w.Write(jsonData) // nolint
	}))
}
