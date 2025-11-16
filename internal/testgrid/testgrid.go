package testgrid

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/prow"
)

var (
	URL            = "https://testgrid.k8s.io"
	e2eSuitePrefix = `Kubernetes e2e suite.`
	kubetestPrefix = `kubetest`
	testRegex      = e2eSuitePrefix + `\[It\] \[(\w.*)\] (?<TEST>\w.*)`
)

const tabURL = "%s/%s/table?tab=%s&exclude-non-failed-tests=&dashboard=%s"

// TestGroup serializes the content from testgrid tab endpoint
type TestGroup struct {
	TestGroupName      string     `json:"test-group-name"`
	Query              string     `json:"query"`
	Status             string     `json:"status"`
	Changelists        []string   `json:"changelists"`
	ColumnIds          []string   `json:"column_ids"`
	CustomColumns      [][]string `json:"custom-columns"`
	ColumnHeaderNames  []string   `json:"column-header-names"`
	Groups             []string   `json:"groups"`
	Tests              []Test
	RowIds             []string `json:"row_ids"`
	Timestamps         []int64  `json:"timestamps"`
	StaleTestThreshold int      `json:"stale-test-threshold"`
	NumStaleTests      int      `json:"num-stale-tests"`
	Description        string   `json:"description"`
	OverallStatus      int      `json:"overall-status"`
}

type Test struct {
	Name         string     `json:"name"`
	OriginalName string     `json:"original-name"`
	Messages     []string   `json:"messages"`
	ShortTexts   []string   `json:"short_texts"`
	Statuses     []Statuses `json:"statuses"`
	Target       string     `json:"target"`
}

type Statuses struct {
	Count int `json:"count"`
	Value int `json:"value"`
}

// RenderStatuses renders the statuses of a test into a string.
func (te *Test) RenderStatuses(timestamps []int64) (string, int, int) {
	var firstFailureIndex = -1
	var failureCount = 0
	var output strings.Builder

	for i, shortText := range te.ShortTexts {
		if shortText == "" {
			continue
		}

		if firstFailureIndex < 0 {
			firstFailureIndex = i
		}

		formattedStatus := formatTestStatus(shortText, timestamps[i], te.Messages[i])
		output.WriteString(formattedStatus)
		failureCount++
	}

	return output.String(), failureCount, firstFailureIndex
}

type TestGrid struct {
	URL string
}

func NewTestGrid(url string) *TestGrid {
	return &TestGrid{URL: url}
}

type DashboardMapper map[string]*v1alpha1.DashboardSummary

// FetchTabSummary retrieves the summary data for a given dashboard from the TestGrid
func (t *TestGrid) FetchTabSummary(dashboard string, filterStatus []string) (summary []v1alpha1.DashboardSummary, err error) {
	var response *http.Response
	url := fmt.Sprintf("%s/%s/summary", t.URL, cleanHTMLCharacters(dashboard))

	// request summary data from TestGrid
	if response, err = http.Get(url); err != nil {
		return nil, fmt.Errorf("error fetching testgrid dashboard summary endpoint: %v", err)
	}

	var data []byte
	if data, err = io.ReadAll(response.Body); err != nil {
		return nil, fmt.Errorf("error parsing body response: %v", err)
	}

	// unmarshal summary data into a struct
	var dashboardList DashboardMapper
	if err = json.Unmarshal(data, &dashboardList); err != nil {
		return nil, fmt.Errorf("error unmarshaling body response: %v", err)
	}

	return filterDashboards(dashboardList, t.URL, filterStatus), nil
}

func filterDashboards(dashboardList DashboardMapper, url string, filterStatus []string) (summary []v1alpha1.DashboardSummary) {
	// iterate and save the final value filtering by status
	// and enhance tab payload
	for tabName, dashboardSummary := range dashboardList {
		if hasStatus(dashboardSummary.OverallState, filterStatus) {
			dashboardSummary.DashboardURL = url
			if dashboardSummary.DashboardTab == nil {
				dashName := dashboardSummary.DashboardName
				dashboardSummary.DashboardTab = &v1alpha1.DashboardTab{
					TabURL:  cleanHTMLCharacters(fmt.Sprintf(tabURL, url, dashName, tabName, dashName)),
					TabName: tabName,
				}
			}
			summary = append(summary, *dashboardSummary)
		}
	}
	return summary
}

// FetchTabTests returns the test group related to the tab of a dashboard
func (t *TestGrid) FetchTabTests(summary *v1alpha1.DashboardSummary, minFailure, minFlake int) (tab *v1alpha1.DashboardTab, err error) {
	var response *http.Response
	if response, err = http.Get(summary.DashboardTab.TabURL); err != nil {
		return tab, err
	}

	var data []byte
	if data, err = io.ReadAll(response.Body); err != nil {
		return tab, err
	}

	// unmarshal test group and be converted into the internal dashboard format
	var testGroup = &TestGroup{}
	if err = json.Unmarshal(data, testGroup); err != nil {
		return tab, err
	}

	aggregation := fmt.Sprintf("%s#%s", summary.DashboardName, summary.DashboardTab.TabName)
	icon := ":large_purple_square:"
	if summary.OverallState == v1alpha1.FAILING_STATUS {
		icon = ":large_red_square:"
	}

	summary.DashboardTab.BoardHash = aggregation
	summary.DashboardTab.TabURL = cleanHTMLCharacters(fmt.Sprintf("https://testgrid.k8s.io/%s&exclude-non-failed-tests=", aggregation))
	summary.DashboardTab.TestRuns = filterTabTests(testGroup, summary.OverallState, minFailure, minFlake)
	summary.DashboardTab.TabState = summary.OverallState
	summary.DashboardTab.StateIcon = icon

	return summary.DashboardTab, nil
}

func filterTabTests(testGroup *TestGroup, state string, minFailure, minFlake int) (tests []v1alpha1.TestResult) {
	jobName := strings.Split(testGroup.Query, "/")
	for _, test := range testGroup.Tests {
		errMessage, failures, firstFailure := test.RenderStatuses(testGroup.Timestamps)
		if (failures >= minFailure && state == v1alpha1.FAILING_STATUS) ||
			(failures >= minFlake && state == v1alpha1.FLAKY_STATUS) {
			testName := test.Name
			if strings.Contains(testName, e2eSuitePrefix) {
				testName = prow.GetRegexParameter(testRegex, testName)["TEST"]
			}
			if strings.Contains(testName, kubetestPrefix) {
				testName = strings.TrimPrefix(strings.TrimPrefix(testName, "kubetest2."), "kubetest.")
			}
			tests = append(tests, v1alpha1.TestResult{
				TestName:        test.Name,
				LatestTimestamp: testGroup.Timestamps[0],
				FirstTimestamp:  testGroup.Timestamps[len(testGroup.Timestamps)-1],
				ProwJobURL:      cleanHTMLCharacters(fmt.Sprintf("https://prow.k8s.io/view/gs/%s/%s", testGroup.Query, testGroup.Changelists[firstFailure])),
				TriageURL:       cleanHTMLCharacters(fmt.Sprintf("https://storage.googleapis.com/k8s-triage/index.html?job=%s$&test=%s", cleanHTMLCharacters(jobName[len(jobName)-1]), cleanHTMLCharacters(testName))),
				ErrorMessage:    errMessage,
			})
		}
	}
	return tests
}

func hasStatus(boardStatus string, statuses []string) bool {
	for _, status := range statuses {
		if boardStatus == status {
			return true
		}
	}
	return false
}

// formatTestStatus creates a formatted string for a single test status.
func formatTestStatus(shortText string, timestamp int64, message string) string {
	timeFormatted := time.Unix(timestamp/1000, 0)
	return fmt.Sprintf("\t%s %s %s\n", shortText, timeFormatted, message)
}

func cleanHTMLCharacters(str string) string {
	return strings.ReplaceAll(str, " ", "%20")
}
