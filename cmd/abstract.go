/* Copyright 2025 Amim Knabben */

package cmd

import (
	"fmt"
	"os"
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

var (
	tg                   = testgrid.NewTestGrid(testgrid.URL)
	minFailure, minFlake int
	refreshInterval      int
	token                string
)

func init() {
	rootCmd.AddCommand(abstractCmd)

	abstractCmd.PersistentFlags().IntVarP(&minFailure, "min-failure", "f", 2, "minimum threshold for test failures")
	abstractCmd.PersistentFlags().IntVarP(&minFlake, "min-flake", "m", 3, "minimum threshold for test flakeness")
	abstractCmd.PersistentFlags().IntVarP(&refreshInterval, "refresh-interval", "r", 0,
		"refresh interval in seconds (0 to disable auto-refresh)")

	token = os.Getenv("SIGNALHOUND_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
}

// FetchTabSummary fetches all dashboard tabs from TestGrid.
func FetchTabSummary() ([]*v1alpha1.DashboardTab, error) {
	var dashboardTabs []*v1alpha1.DashboardTab
	for _, dashboard := range []string{"sig-release-master-blocking", "sig-release-master-informing"} {
		dashSummaries, err := tg.FetchTabSummary(dashboard, v1alpha1.ERROR_STATUSES)
		if err != nil {
			return nil, err
		}
		for _, dashSummary := range dashSummaries {
			dashTab, err := tg.FetchTabTests(&dashSummary, minFailure, minFlake)
			if err != nil {
				fmt.Println(fmt.Errorf("error fetching table : %s", err))
				continue
			}
			if len(dashTab.TestRuns) > 0 {
				dashboardTabs = append(dashboardTabs, dashTab)
			}
		}
	}
	return dashboardTabs, nil
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
