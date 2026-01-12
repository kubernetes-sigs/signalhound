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
2. Review the existing GitHub issues, paying special attention to:
   - Issues from the SAME board/tab (BoardHash) as the failing test
   - Issues that might cover the same test or related tests in the same category
   - Issues that mention similar test names, SIGs, or error patterns
3. For each failing/flaky test, determine if it is:
   a) ALREADY COVERED: An existing issue on the same board/tab likely covers this test (same test name, similar test pattern, same SIG, or broader category issue)
   b) MISSING: No existing issue covers this test
4. Report both categories clearly

Currently Failing and Flaky Tests:
%s

Existing GitHub Issues:
%s

Please provide your analysis in the following format, only showing the MISSING items:

## Tests Missing Issues

For each test that does NOT have a corresponding issue:
- Test name and board/tab
- Brief summary of what test is failing and why it needs an issue

Sort by: Status then Test name

IMPORTANT: When determining if a test is "COVERED", be liberal in your interpretation:

1. **Board/Tab Matching**: 
   - Extract board/tab information from issue titles and bodies (look for patterns like "sig-release-master-blocking", "board#tab", or similar board names)
   - If an issue mentions the same board/tab (BoardHash) as a failing test, it's likely related
   - Board names can appear in various formats: "sig-release-master-blocking", "sig-release-master-informing", etc.

2. **Test Name Matching**:
   - Exact test name match = COVERED
   - Similar test name (same prefix, same SIG) = COVERED
   - Test mentioned in a broader category issue = COVERED

3. **SIG Matching**:
   - If an issue exists for the same board/tab with the same SIG, consider tests from that SIG potentially covered
   - Look for SIG mentions in issue titles/bodies (e.g., "[sig-node]", "SIG Node", etc.)

4. **Broader Coverage**:
   - If an issue exists for the same board/tab and covers a broader category (e.g., "multiple tests failing", "job failing", "tab failing"), consider related tests covered
   - Issues that mention the board/tab but not specific tests may still cover all tests in that board/tab

5. **When to mark as MISSING**:
   - Only mark as MISSING if you're confident no existing issue on the same board/tab could reasonably cover it
   - If unsure, prefer marking as COVERED to avoid duplicate issues

**Example**: If a test "TestA" is failing on board "sig-release-master-blocking#gce-cos-master-default" and there's an issue titled "[Failing Test] sig-release-master-blocking#gce-cos-master-default - multiple tests failing", then TestA should be marked as COVERED even if it's not explicitly mentioned.`

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

	// Build a list of failing tests for comparison with clear board/tab grouping
	var brokenTestsList strings.Builder
	brokenTestsList.WriteString("=== Currently Failing or Flaking Tests ===\n\n")
	for _, tab := range failingTabs {
		// Extract board name and tab name from BoardHash (format: "board#tab")
		boardParts := strings.Split(tab.BoardHash, "#")
		boardName := boardParts[0]
		tabName := ""
		if len(boardParts) > 1 {
			tabName = boardParts[1]
		}

		brokenTestsList.WriteString(fmt.Sprintf("Board/Tab: %s (BoardHash: %s)\n", tab.BoardHash, tab.BoardHash))
		brokenTestsList.WriteString(fmt.Sprintf("  Board Name: %s\n", boardName))
		if tabName != "" {
			brokenTestsList.WriteString(fmt.Sprintf("  Tab Name: %s\n", tabName))
		}
		brokenTestsList.WriteString(fmt.Sprintf("  Status: %s\n", tab.TabState))
		brokenTestsList.WriteString("  Tests:\n")
		for _, test := range tab.TestRuns {
			brokenTestsList.WriteString(fmt.Sprintf("    - %s\n", test.TestName))
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
	if len(message.Content) > 0 {
		for _, block := range message.Content {
			if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
				response += textBlock.Text
			}
		}
	} else {
		// Fallback if Anthropic fails
		_ = fmt.Sprintf("=== Analysis ===\n\nFlake || Failing Tests:\n%s\n\nExisting Issues:\n%s", brokenTestsList.String(), issuesText)
	}

	return response, nil
}
