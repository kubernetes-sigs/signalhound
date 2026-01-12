/* Copyright 2025 Amim Knabben */

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/mcp"
	"sigs.k8s.io/signalhound/internal/testgrid"
	"sigs.k8s.io/signalhound/internal/tui"
)

const MCP_SERVER = "localhost:8080"

// abstractCmd represents the abstract command
var abstractCmd = &cobra.Command{
	Use:   "abstract",
	Short: "Summarize the board status and present the flake or failing ones",
	RunE:  RunAbstract,
}

var (
	minFailure, minFlake int
	token                string
)

func init() {
	rootCmd.AddCommand(abstractCmd)

	abstractCmd.PersistentFlags().IntVarP(&minFailure, "min-failure", "f", 0,
		"minimum threshold for test failures, to disable use 0. Defaults to 0.")
	abstractCmd.PersistentFlags().IntVarP(&minFlake, "min-flake", "m", 0,
		"minimum threshold for test flakeness, to disable use 0. Defaults to 0.")

	token = os.Getenv("SIGNALHOUND_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
}

// RunAbstract starts the main command to scrape TestGrid.
func RunAbstract(cmd *cobra.Command, args []string) error {
	var (
		tg            = testgrid.NewTestGrid(testgrid.URL)
		dashboardTabs []*v1alpha1.DashboardTab
		dashboards    = []string{"sig-release-master-blocking", "sig-release-master-informing"}
	)

	// start the MCP server in the background
	go startMCPServer()

	fmt.Println("Scraping the testgrid dashboard, wait...")
	tuiInstance := tui.NewMultiWindowTUI(dashboardTabs, token)
	tuiInstance.SetRefreshConfig(tg, dashboards, minFailure, minFlake)
	return tuiInstance.Run()
}

// startMCPServer starts the MCP server in the background
func startMCPServer() {
	ctx := context.Background()
	mcpToken := os.Getenv("ANTHROPIC_API_KEY")
	if mcpToken == "" {
		log.Println("Warning: ANTHROPIC_API_KEY not set, MCP server will not start")
		return
	}
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		log.Println("Warning: GITHUB_TOKEN not set, MCP server will not start")
		return
	}

	server := mcp.NewMCPServer(ctx, githubToken)
	log.Printf("MCP handler listening at %s", MCP_SERVER)
	if err := http.ListenAndServe(MCP_SERVER, server.NewHTTPHandler()); err != nil {
		log.Printf("MCP server error: %v", err)
	}
}
