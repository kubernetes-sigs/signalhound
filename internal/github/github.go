package github

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	g4 "github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

const (
	PROJECT_ID   = "PVT_kwDOAM_34M4AAThW"
	ORGANIZATION = "kubernetes"
)

type ProjectManagerInterface interface {
	GetProjectFields() ([]ProjectFieldInfo, error)
	CreateDraftIssue(title, body, board string) error
	GetProjectIssues(perPage int) ([]Issue, error)
}

// ProjectManager represents a GitHub organization with a global workflow file and reference
type ProjectManager struct {
	// organization is the GitHub organization name
	organization string

	// projectID is the ID of the Kubernetes version project board
	projectID string

	// fields is a map of project field names to their IDs
	fields map[string]ProjectFieldInfo

	// githubClient is the official GitHub API v4 (GraphQL) client
	githubClient *g4.Client
}

// ProjectFieldInfo represents a project field with its options
type ProjectFieldInfo struct {
	ID      g4.ID
	Name    g4.String
	Options map[string]interface{} // option name -> option ID
}

// Project represents a GitHub project
type Project struct {
	Name    *string
	Body    *string
	State   *string
	HTMLURL *string
}

// Issue represents a GitHub issue on a project board
type Issue struct {
	Number  int
	Title   string
	Body    string
	State   string
	HTMLURL string
}

// NewProjectManager creates a new ProjectManager
func NewProjectManager(ctx context.Context, token string) ProjectManagerInterface {
	return &ProjectManager{
		organization: ORGANIZATION,
		projectID:    PROJECT_ID,
		fields:       map[string]ProjectFieldInfo{},
		githubClient: g4.NewClient(oauth2.NewClient(
			ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
		)),
	}
}

// GetProjectFields queries the project fields and their options
func (g *ProjectManager) GetProjectFields() ([]ProjectFieldInfo, error) {
	if g.githubClient == nil {
		return nil, errors.New("github GraphQL client is nil")
	}

	var query struct {
		Node struct {
			ProjectV2 struct {
				Fields struct {
					Nodes []struct {
						Typename string `graphql:"__typename"`
						// Single select field
						ProjectV2SingleSelectField struct {
							ID      g4.ID
							Name    g4.String
							Options []struct {
								ID   g4.ID
								Name g4.String
							}
						} `graphql:"... on ProjectV2SingleSelectField"`
						// Iteration field
						ProjectV2IterationField struct {
							ID   g4.ID
							Name g4.String
						} `graphql:"... on ProjectV2IterationField"`
					}
				} `graphql:"fields(first: 50)"`
			} `graphql:"... on ProjectV2"`
		} `graphql:"node(id: $projectID)"`
	}

	variables := map[string]interface{}{
		"projectID": g4.ID(g.projectID),
	}

	if err := g.githubClient.Query(context.Background(), &query, variables); err != nil {
		return nil, fmt.Errorf("failed to query project fields: %w", err)
	}

	fields := make([]ProjectFieldInfo, 0, len(query.Node.ProjectV2.Fields.Nodes))

	for _, node := range query.Node.ProjectV2.Fields.Nodes {
		var fieldID g4.ID
		var fieldName g4.String
		options := make(map[string]interface{})

		// Handle different field types based on __typename
		switch node.Typename {
		case "ProjectV2SingleSelectField":
			fieldID = node.ProjectV2SingleSelectField.ID
			fieldName = node.ProjectV2SingleSelectField.Name
			for _, opt := range node.ProjectV2SingleSelectField.Options {
				options[string(opt.Name)] = opt.ID
			}
		case "ProjectV2IterationField":
			fieldID = node.ProjectV2IterationField.ID
			fieldName = node.ProjectV2IterationField.Name
		default:
			continue
		}

		fields = append(fields, ProjectFieldInfo{
			ID:      fieldID,
			Name:    fieldName,
			Options: options,
		})
	}

	return fields, nil
}

// CreateDraftIssue creates a new issue draft issue in the board with a
// specific test issue template.
func (g *ProjectManager) CreateDraftIssue(title, body, board string) error {
	if g.githubClient == nil {
		return errors.New("github GraphQL client is nil")
	}

	// first, get the project fields to find the correct field IDs and option IDs
	fields, err := g.GetProjectFields()
	if err != nil {
		return fmt.Errorf("failed to get project fields: %w", err)
	}

	// find the fields we need
	var k8sReleaseFieldID, viewFieldID, statusFieldID, boardFieldID g4.ID
	var k8sReleaseValueID, viewValueID, statusValueID, boardValueID g4.ID

	// Use helper function to find k8s_release field and latest version
	k8sReleaseFieldID, k8sReleaseValueID = findK8sReleaseFieldAndLatestVersion(fields)

	// Use helper function to find status field with "drafting" or "draft" option
	statusFieldID, statusValueID = findStatusFieldAndOption(fields, func(optName string) bool {
		optNameLower := strings.ToLower(optName)
		return strings.Contains(optNameLower, "drafting") || strings.Contains(optNameLower, "draft")
	})

	for _, field := range fields {
		fieldNameLower := strings.ToLower(string(field.Name))

		// find view field - look for fields containing "view"
		if strings.Contains(fieldNameLower, "view") {
			viewFieldID = field.ID
			// find "issue-tracking" option
			for optName, optID := range field.Options {
				if strings.Contains(strings.ToLower(optName), "issue-tracking") ||
					strings.Contains(strings.ToLower(optName), "issue tracking") {
					viewValueID = optID
					break
				}
			}
		}

		// find the board field, master-informing or master-blocking
		if strings.Contains(fieldNameLower, "board") {
			boardFieldID = field.ID
			for optName, optID := range field.Options {
				if strings.Contains(board, strings.ToLower(optName)) {
					boardValueID = optID
					break
				}
			}
		}
	}

	// create the draft issue
	var mutationDraft struct {
		AddProjectV2DraftIssue struct {
			ProjectItem struct {
				ID g4.ID
			}
		} `graphql:"addProjectV2DraftIssue(input: $input)"`
	}
	bodyInput := g4.String(body)
	inputDraft := g4.AddProjectV2DraftIssueInput{
		ProjectID: g4.ID(g.projectID),
		Title:     g4.String(title),
		Body:      &bodyInput,
	}

	if err := g.githubClient.Mutate(context.Background(), &mutationDraft, inputDraft, nil); err != nil {
		return fmt.Errorf("failed to create draft issue: %w", err)
	}

	itemID := mutationDraft.AddProjectV2DraftIssue.ProjectItem.ID
	var mutationUpdate struct {
		UpdateProjectV2ItemFieldValue struct {
			ClientMutationID string
		} `graphql:"updateProjectV2ItemFieldValue(input: $input)"`
	}

	fieldUpdates := []struct {
		fieldID   g4.ID
		optionID  g4.ID
		fieldName string
	}{
		{k8sReleaseFieldID, k8sReleaseValueID, "K8s Release"},
		{viewFieldID, viewValueID, "View"},
		{statusFieldID, statusValueID, "Status"},
		{boardFieldID, boardValueID, "Testgrid Board"},
	}

	for _, update := range fieldUpdates {
		if update.fieldID != "" && update.optionID != "" {
			optionIDStr := fmt.Sprintf("%s", update.optionID)
			if err := g.githubClient.Mutate(context.Background(), &mutationUpdate, g4.UpdateProjectV2ItemFieldValueInput{
				ProjectID: g4.ID(g.projectID),
				ItemID:    itemID,
				FieldID:   update.fieldID,
				Value:     g4.ProjectV2FieldValue{SingleSelectOptionID: (*g4.String)(&optionIDStr)},
			}, nil); err != nil {
				fmt.Printf("Warning: failed to update %s field: %v\n", update.fieldName, err)
			}
		}
	}
	return nil
}

// GetProjectIssues retrieves all issues from the project board
func (g *ProjectManager) GetProjectIssues(perPage int) ([]Issue, error) {
	if g.githubClient == nil {
		return nil, errors.New("github GraphQL client is nil")
	}

	// Get project fields to find the k8s_release field ID
	fields, err := g.GetProjectFields()
	if err != nil {
		return nil, fmt.Errorf("failed to get project fields: %w", err)
	}

	// Use helper functions to find fields
	k8sReleaseFieldID, k8sReleaseOptionID := findK8sReleaseFieldAndLatestVersion(fields)
	statusFieldID, failingStatusOptionID := findStatusFieldAndOption(fields, func(optName string) bool {
		return strings.Contains(strings.ToLower(optName), "failing") ||
			strings.Contains(strings.ToLower(optName), "flaky")
	})

	if k8sReleaseOptionID == "" {
		return nil, fmt.Errorf("latest version option not found in k8s_release field")
	}

	if failingStatusOptionID == "" {
		return nil, fmt.Errorf("FAILING status option not found in status field")
	}

	// Find the latest version string for comparison
	var latestVersionStr string
	for _, field := range fields {
		fieldNameLower := strings.ToLower(string(field.Name))
		if strings.Contains(fieldNameLower, "k8s release") && field.ID == k8sReleaseFieldID {
			for optName, optID := range field.Options {
				if optID == k8sReleaseOptionID {
					latestVersionStr = extractVersion(optName)
					break
				}
			}
			break
		}
	}

	issues := make([]Issue, 0)
	var cursor *g4.String
	hasNextPage := true

	for hasNextPage {
		var query struct {
			Node struct {
				ProjectV2 struct {
					Items struct {
						Nodes []struct {
							Content struct {
								Typename string `graphql:"__typename"`
								Issue    struct {
									Number g4.Int
									Title  g4.String
									Body   g4.String
									State  g4.IssueState
									URL    g4.URI
								} `graphql:"... on Issue"`
							}
							FieldValues struct {
								Nodes []struct {
									Typename                            string `graphql:"__typename"`
									ProjectV2ItemFieldSingleSelectValue struct {
										Field struct {
											ProjectV2FieldCommon struct {
												ID   g4.ID
												Name g4.String
											} `graphql:"... on ProjectV2FieldCommon"`
										} `graphql:"field"`
										Name g4.String
									} `graphql:"... on ProjectV2ItemFieldSingleSelectValue"`
								}
							} `graphql:"fieldValues(first: 20)"`
						}
						PageInfo struct {
							HasNextPage g4.Boolean
							EndCursor   g4.String
						}
					} `graphql:"items(first: $first, after: $after)"`
				} `graphql:"... on ProjectV2"`
			} `graphql:"node(id: $projectID)"`
		}

		// Note: GitHub GraphQL API does NOT support filter parameter for ProjectV2 items
		// We need to fetch all items and filter them in code by checking fieldValues
		variables := map[string]interface{}{
			"projectID": g4.ID(g.projectID),
			"first":     g4.Int(perPage),
			"after":     cursor,
		}

		if err := g.githubClient.Query(context.Background(), &query, variables); err != nil {
			return nil, fmt.Errorf("failed to query project issues: %w", err)
		}

		// Filter items by k8s_release field value in code
		// Since GraphQL API doesn't support filter parameter, we fetch all and filter manually
		for _, node := range query.Node.ProjectV2.Items.Nodes {
			// Only process actual issues, not draft issues or pull requests
			if node.Content.Typename != "Issue" {
				continue
			}

			// Check if this item has the matching k8s_release field value and FAILING status
			matchesVersion := false
			matchesStatus := false

			for _, fieldValue := range node.FieldValues.Nodes {
				if fieldValue.Typename == "ProjectV2ItemFieldSingleSelectValue" {
					fieldID := fmt.Sprintf("%v", fieldValue.ProjectV2ItemFieldSingleSelectValue.Field.ProjectV2FieldCommon.ID)
					optionName := string(fieldValue.ProjectV2ItemFieldSingleSelectValue.Name)

					// Check if this is the k8s_release field with the latest version
					if fieldID == fmt.Sprintf("%v", k8sReleaseFieldID) {
						// Extract version and check if it matches the latest version we found
						extractedVersion := extractVersion(optionName)
						if extractedVersion == latestVersionStr {
							matchesVersion = true
						}
					}

					// Check if this is the status field with FAILING status
					if fieldID == fmt.Sprintf("%v", statusFieldID) {
						optionNameLower := strings.ToLower(optionName)
						if strings.Contains(optionNameLower, "failing") || strings.Contains(optionNameLower, "flaky") {
							matchesStatus = true
						}
					}
				}
			}

			// Only include issues that match both the version filter and FAILING status
			if matchesVersion && matchesStatus {
				issue := Issue{
					Number:  int(node.Content.Issue.Number),
					Title:   string(node.Content.Issue.Title),
					Body:    string(node.Content.Issue.Body),
					State:   string(node.Content.Issue.State),
					HTMLURL: node.Content.Issue.URL.String(),
				}
				issues = append(issues, issue)
			}
		}

		hasNextPage = bool(query.Node.ProjectV2.Items.PageInfo.HasNextPage)
		if hasNextPage {
			cursor = &query.Node.ProjectV2.Items.PageInfo.EndCursor
		}
	}

	return issues, nil
}

// findK8sReleaseFieldAndLatestVersion finds the k8s_release field and returns the field ID and latest version option ID
func findK8sReleaseFieldAndLatestVersion(fields []ProjectFieldInfo) (fieldID g4.ID, optionID g4.ID) {
	for _, field := range fields {
		fieldNameLower := strings.ToLower(string(field.Name))
		if strings.Contains(fieldNameLower, "k8s release") {
			fieldID = field.ID
			// find the latest version option (highest version number)
			latestVersion := ""
			latestVersionID := g4.ID("")
			for optName, optID := range field.Options {
				// extract version number from option name (e.g., "v1.32" -> "1.32")
				if version := extractVersion(optName); version != "" {
					if latestVersion == "" || compareVersions(version, latestVersion) > 0 {
						latestVersion = version
						if id, ok := optID.(g4.ID); ok {
							latestVersionID = id
						}
					}
				}
			}
			if latestVersionID != g4.ID("") {
				optionID = latestVersionID
			}
			break
		}
	}
	return
}

// findStatusFieldAndOption finds the status field and returns the field ID and option ID matching the criteria
func findStatusFieldAndOption(fields []ProjectFieldInfo, optionMatcher func(string) bool) (fieldID g4.ID, optionID g4.ID) {
	for _, field := range fields {
		fieldNameLower := strings.ToLower(string(field.Name))
		if strings.Contains(fieldNameLower, "status") {
			fieldID = field.ID
			// Find the option that matches the criteria
			for optName, optID := range field.Options {
				if optionMatcher(optName) {
					if id, ok := optID.(g4.ID); ok {
						optionID = id
						break
					}
				}
			}
			break
		}
	}
	return
}

// compareVersions compares two version strings (e.g., "1.30", "1.31")
// Returns: 1 if v1 > v2, -1 if v1 < v2, 0 if equal
func compareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var num1, num2 int
		if i < len(parts1) {
			num1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			num2, _ = strconv.Atoi(parts2[i])
		}

		if num1 > num2 {
			return 1
		}
		if num1 < num2 {
			return -1
		}
	}

	return 0
}

// extractVersion extracts a version string from text (e.g., "v1.32" -> "1.32", "1.30" -> "1.30")
func extractVersion(text string) string {
	versionPattern := regexp.MustCompile(`v?(\d+)\.(\d+)`)
	if matches := versionPattern.FindStringSubmatch(text); len(matches) >= 3 {
		return fmt.Sprintf("%s.%s", matches[1], matches[2])
	}
	return ""
}
