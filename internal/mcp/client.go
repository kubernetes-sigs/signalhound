package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"sigs.k8s.io/signalhound/api/v1alpha1"
)

const templatePrompt = `You are analyzing a list of currently failing and flaky Kubernetes tests and comparing them with existing GitHub project issues.

Your task is to:
1. Review the list of currently failing and flaky tests
2. Review the existing GitHub issues
3. Identify which failing AND flake tests does NOT have a corresponding GitHub issue created yet
4. For each MISSING issue, provide a brief summary of what test is failing and why it needs an issue

Currently Failing and Flaky Tests:
%s

Existing GitHub Issues:
%s

Please provide:
1. A list of tests that are failing but don't have corresponding GitHub issues.
2. A brief summary table missing tests.
3. Sort them by status and name`

type MCPClient struct {
	ctx context.Context

	mcpEndpoint     string
	anthropicAPIKey string
	clientSession   *mcp.ClientSession
}

func NewMCPClient(anthropicAPIKey, mcpEndpoint string) (*MCPClient, error) {
	ctx := context.Background()
	impl := &mcp.Implementation{
		Name:    "signalhound-tui",
		Version: "1.0.0",
	}
	client := mcp.NewClient(impl, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: mcpEndpoint,
	}
	clientSession, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}

	return &MCPClient{
		ctx:             ctx,
		clientSession:   clientSession,
		anthropicAPIKey: anthropicAPIKey,
		mcpEndpoint:     mcpEndpoint,
	}, nil
}

// LoadGithubIssues loads the list of GitHub issues for the given tabs
func (m *MCPClient) LoadGithubIssues(tabs []*v1alpha1.DashboardTab) (string, error) {
	// Filter for only FAILING_STATUS and FLAKY_STATUS tabs
	failingTabs := make([]*v1alpha1.DashboardTab, 0)
	for _, tab := range tabs {
		if tab.TabState == v1alpha1.FAILING_STATUS || tab.TabState == v1alpha1.FLAKY_STATUS {
			failingTabs = append(failingTabs, tab)
		}
	}

	// Directly call the MCP tool to get issues
	params := &mcp.CallToolParams{
		Name: "list_project_issues",
		Arguments: map[string]interface{}{
			"perPage": 100,
		},
	}

	result, err := m.clientSession.CallTool(m.ctx, params)
	if err != nil {
		return "", err
	}

	// Parse the issues from the MCP response
	var issuesText string
	if result.IsError {
		issuesText = "Error: Tool returned an error\n"
	} else {
		for _, content := range result.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				issuesText += textContent.Text + "\n"
			}
		}
	}

	// Build a list of failing tests for comparison
	var brokenTestsList strings.Builder
	brokenTestsList.WriteString("=== Currently Failing or Flaking Tests ===\n\n")
	for _, tab := range failingTabs {
		brokenTestsList.WriteString(fmt.Sprintf("Board: %s (%s)\n", tab.BoardHash, tab.TabName))
		for _, test := range tab.TestRuns {
			brokenTestsList.WriteString(fmt.Sprintf("  - Test: %s, Status: %s\n", test.TestName, tab.TabState))
			if test.ErrorMessage != "" {
				brokenTestsList.WriteString(fmt.Sprintf("    Error: %s\n", test.ErrorMessage))
			}
		}
		brokenTestsList.WriteString("\n")
	}

	// Use Anthropic to compare failing tests with existing issues and identify missing ones
	anthropicClient := anthropic.NewClient(
		option.WithAPIKey(m.anthropicAPIKey),
	)

	prompt := fmt.Sprintf(templatePrompt, brokenTestsList.String(), issuesText)

	message, err := anthropicClient.Messages.New(m.ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_5_20250929,
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", err
	}

	var response string
	if err == nil && len(message.Content) > 0 {
		for _, block := range message.Content {
			if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
				response += textBlock.Text
			}
		}
	} else {
		// Fallback if Anthropic fails
		response = fmt.Sprintf("=== Analysis ===\n\nFlake || Failing Tests:\n%s\n\nExisting Issues:\n%s", brokenTestsList.String(), issuesText)
	}

	return response, nil
}
