package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"sigs.k8s.io/signalhound/internal/github"
)

// MCPServer represents an MCP server instance
type MCPServer struct {
	githubClient github.ProjectManagerInterface
	ctx          context.Context
	server       *mcp.Server
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(ctx context.Context, githubToken string) *MCPServer {
	server := &MCPServer{
		githubClient: github.NewProjectManager(ctx, githubToken),
		ctx:          ctx,
	}

	// create MCP server with implementation info
	impl := &mcp.Implementation{
		Name:    "signalhound",
		Version: "1.0.0",
	}

	server.server = mcp.NewServer(impl, nil)

	// Add tools
	listIssuesSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"perPage": {
				"type": "number",
				"description": "Number of issues per page (default: 100)"
			}
		}
	}`)

	mcp.AddTool(server.server, &mcp.Tool{
		Name:        "list_project_issues",
		Description: "List all issues from the SIG Signal project board",
		InputSchema: listIssuesSchema,
	}, server.handleListProjectIssues)

	return server
}

// NewHTTPHandler creates an HTTP handler for StreamableHTTP transport
func (s *MCPServer) NewHTTPHandler() *mcp.StreamableHTTPHandler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.server
	}, nil)
}

// ListProjectIssuesInput represents the input for list_project_issues tool
type ListProjectIssuesInput struct {
	PerPage int `json:"perPage,omitempty"`
}

// handleListProjectIssues handles the list_project_issues tool call
func (s *MCPServer) handleListProjectIssues(ctx context.Context, req *mcp.CallToolRequest, input ListProjectIssuesInput) (
	*mcp.CallToolResult,
	any,
	error,
) {
	// Parse arguments
	perPage := 100
	if input.PerPage > 0 {
		perPage = input.PerPage
	}

	issues, err := s.githubClient.GetProjectIssues(perPage)
	if err != nil {
		log.Printf("Error getting issues: %v", err)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Failed to get issues: %v", err)},
			},
			IsError: true,
		}, nil, nil
	}

	var resultText string
	if len(issues) == 0 {
		resultText = "No issues found on the project board"
	} else {
		resultText = fmt.Sprintf("Found %d issue(s) on the project board:\n\n", len(issues))
		for i, issue := range issues {
			body := issue.Body
			if len(body) > 200 {
				body = body[:200] + "..."
			}
			resultText += fmt.Sprintf("%d. #%d: %s\n", i+1, issue.Number, issue.Title)
			resultText += fmt.Sprintf("   State: %s\n", issue.State)
			resultText += fmt.Sprintf("   URL: %s\n", issue.HTMLURL)
			if body != "" {
				resultText += fmt.Sprintf("   Body: %s\n", body)
			}
			resultText += "\n"
		}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: resultText},
		},
	}, nil, nil
}
