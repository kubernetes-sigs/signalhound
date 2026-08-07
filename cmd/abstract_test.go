/* Copyright 2026 The Kubernetes Authors. */

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/testgrid"
)

const (
	testDashboardName  = "test-dashboard"
	testgridStatusFail = 12
)

func TestFetchTabSummaryIncludesHistoricalFailuresFromPassingTabs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/test-dashboard/summary" {
			if err := json.NewEncoder(writer).Encode(testgrid.DashboardMapper{
				"test-tab": {
					OverallState:  v1alpha1.PASSING_STATUS,
					DashboardName: testDashboardName,
				},
			}); err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		if err := json.NewEncoder(writer).Encode(testgrid.TestGroup{
			Query:       "kubernetes-ci-logs/logs/test-job",
			Timestamps:  []int64{1_758_999_193_000},
			Changelists: []string{"1972011571991285760"},
			Tests: []testgrid.Test{
				{
					Name:       "historical failure",
					ShortTexts: []string{"F"},
					Messages:   []string{"failed"},
					Statuses:   []testgrid.Statuses{{Count: 1, Value: testgridStatusFail}},
				},
			},
		}); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	previousTestGrid := tg
	previousDashboards := dashboards
	previousMinFailure, previousMinFlake := minFailure, minFlake
	defer func() {
		tg = previousTestGrid
		dashboards = previousDashboards
		minFailure, minFlake = previousMinFailure, previousMinFlake
	}()

	tg = testgrid.NewTestGrid(server.URL)
	dashboards = []string{testDashboardName}
	minFailure, minFlake = 0, 0

	tabs, err := FetchTabSummary()

	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, v1alpha1.FLAKY_STATUS, tabs[0].TabState)
	assert.Len(t, tabs[0].TestRuns, 1)
}

func TestFetchDashboardTabsBoundsConcurrencyAndPreservesOrder(t *testing.T) {
	const summaryCount = maxConcurrentTabFetches + 4

	var activeRequests int32
	var peakRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := atomic.AddInt32(&activeRequests, 1)
		defer atomic.AddInt32(&activeRequests, -1)
		for {
			peak := atomic.LoadInt32(&peakRequests)
			if current <= peak || atomic.CompareAndSwapInt32(&peakRequests, peak, current) {
				break
			}
		}

		index, err := strconv.Atoi(strings.TrimPrefix(request.URL.Path, "/"))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(time.Duration(summaryCount-index) * 10 * time.Millisecond)
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(testgrid.TestGroup{
			Query:       fmt.Sprintf("kubernetes-ci-logs/logs/test-job-%d", index),
			Timestamps:  []int64{1_758_999_193_000},
			Changelists: []string{"1972011571991285760"},
			Tests: []testgrid.Test{
				{
					Name:       "historical failure",
					ShortTexts: []string{"F"},
					Messages:   []string{"failed"},
					Statuses:   []testgrid.Statuses{{Count: 1, Value: testgridStatusFail}},
				},
			},
		}); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	previousTestGrid := tg
	previousMinFailure, previousMinFlake := minFailure, minFlake
	defer func() {
		tg = previousTestGrid
		minFailure, minFlake = previousMinFailure, previousMinFlake
	}()
	tg = testgrid.NewTestGrid(server.URL)
	minFailure, minFlake = 0, 0

	summaries := make([]v1alpha1.DashboardSummary, summaryCount)
	expectedTabNames := make([]string, summaryCount)
	for i := range summaries {
		tabName := fmt.Sprintf("tab-%02d", i)
		expectedTabNames[i] = tabName
		summaries[i] = v1alpha1.DashboardSummary{
			OverallState:  v1alpha1.FLAKY_STATUS,
			DashboardName: testDashboardName,
			DashboardTab: &v1alpha1.DashboardTab{
				TabName: tabName,
				TabURL:  fmt.Sprintf("%s/%d", server.URL, i),
			},
		}
	}

	tabs := fetchDashboardTabs(summaries)

	require.Len(t, tabs, summaryCount)
	actualTabNames := make([]string, len(tabs))
	for i, tab := range tabs {
		actualTabNames[i] = tab.TabName
	}
	assert.Equal(t, expectedTabNames, actualTabNames)
	assert.Greater(t, int(peakRequests), 1)
	assert.LessOrEqual(t, int(peakRequests), maxConcurrentTabFetches)
}
