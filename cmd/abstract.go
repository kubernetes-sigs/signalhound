/* Copyright 2025 Amim Knabben */

package cmd

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/testgrid"
	"sigs.k8s.io/signalhound/internal/tui"
)

// abstractCmd represents the abstract command
var abstractCmd = &cobra.Command{
	Use:   "abstract",
	Short: "Summarize the board status and present the flake or failing ones",
	RunE:  RunAbstract,
}

var defaultDashboards = []string{"sig-release-master-blocking", "sig-release-master-informing"}

const maxConcurrentTabFetches = 8

// Passing tabs can still contain historical failures outside TestGrid's summary window.
var monitoredStatuses = []string{
	v1alpha1.PASSING_STATUS,
	v1alpha1.FAILING_STATUS,
	v1alpha1.FLAKY_STATUS,
}

var (
	tg                   = testgrid.NewTestGrid(testgrid.URL)
	minFailure, minFlake int
	refreshInterval      int
	token                string
	dashboards           []string
)

func init() {
	rootCmd.AddCommand(abstractCmd)

	abstractCmd.PersistentFlags().IntVarP(&minFailure, "min-failure", "f", 0,
		"minimum threshold for test failures, to disable use 0. Defaults to 0.")
	abstractCmd.PersistentFlags().IntVarP(&minFlake, "min-flake", "m", 0,
		"minimum threshold for test flakeness, to disable use 0. Defaults to 0.")
	abstractCmd.PersistentFlags().IntVarP(&refreshInterval, "refresh-interval", "r", 0,
		"refresh interval in seconds (0 to disable auto-refresh)")
	abstractCmd.PersistentFlags().StringSliceVarP(&dashboards, "dashboards", "d", defaultDashboards,
		"comma-separated list of TestGrid dashboards to monitor (e.g. sig-release-1.35-blocking,sig-release-1.35-informing)")

	token = os.Getenv("SIGNALHOUND_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
}

// FetchTabSummary fetches all dashboard tabs from TestGrid.
func FetchTabSummary() ([]*v1alpha1.DashboardTab, error) {
	var dashboardTabs []*v1alpha1.DashboardTab
	for _, dashboard := range dashboards {
		dashSummaries, err := tg.FetchTabSummary(dashboard, monitoredStatuses)
		if err != nil {
			return nil, err
		}
		dashboardTabs = append(dashboardTabs, fetchDashboardTabs(dashSummaries)...)
	}
	return dashboardTabs, nil
}

func fetchDashboardTabs(summaries []v1alpha1.DashboardSummary) []*v1alpha1.DashboardTab {
	type fetchResult struct {
		tab *v1alpha1.DashboardTab
		err error
	}

	results := make([]fetchResult, len(summaries))
	semaphore := make(chan struct{}, maxConcurrentTabFetches)
	var waitGroup sync.WaitGroup

	for i := range summaries {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			results[i].tab, results[i].err = tg.FetchTabTests(&summaries[i], minFailure, minFlake)
		}()
	}
	waitGroup.Wait()

	dashboardTabs := make([]*v1alpha1.DashboardTab, 0, len(results))
	for _, result := range results {
		if result.err != nil {
			fmt.Println(fmt.Errorf("error fetching table: %w", result.err))
			continue
		}
		if len(result.tab.TestRuns) > 0 {
			dashboardTabs = append(dashboardTabs, result.tab)
		}
	}
	return dashboardTabs
}

// RunAbstract starts the main command to scrape TestGrid.
func RunAbstract(cmd *cobra.Command, args []string) error {
	dashboardTabs, err := FetchTabSummary()
	if err != nil {
		return err
	}

	var refreshFunc func() ([]*v1alpha1.DashboardTab, error)
	if refreshInterval > 0 {
		refreshFunc = func() ([]*v1alpha1.DashboardTab, error) {
			return FetchTabSummary()
		}
	}

	return tui.RenderVisual(dashboardTabs, token, time.Duration(refreshInterval)*time.Second, refreshFunc)
}
