package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"sigs.k8s.io/signalhound/api/v1alpha1"
	"sigs.k8s.io/signalhound/internal/github"
	intmcp "sigs.k8s.io/signalhound/internal/mcp"
	"sigs.k8s.io/signalhound/internal/testgrid"
)

const (
	defaultMCPEndpoint     = "http://localhost:8080/mcp"
	errorMsgFormat         = "Error calling MCP tool: %v"
	successMsg             = "[green]✓ Analysis Completed"
	defaultRefreshInterval = 10 * time.Minute
)

// MultiWindowTUI represents the multi-window TUI application
type MultiWindowTUI struct {
	app             *tview.Application
	pages           *tview.Pages
	tabs            []*v1alpha1.DashboardTab
	githubToken     string
	brokenTestsPage *tview.Flex
	mcpIssuesPage   *tview.Flex
	mcpPanelRef     *tview.TextArea
	statusPanelRef  *tview.TextView
	tabsPanelRef    *tview.List
	brokenPanelRef  *tview.List
	slackPanelRef   *tview.TextArea
	githubPanelRef  *tview.TextArea
	positionRef     *tview.TextView
	// Auto-refresh fields
	refreshTicker  *time.Ticker
	testgridClient *testgrid.TestGrid
	dashboards     []string
	minFailure     int
	minFlake       int
	refreshStopCh  chan struct{}
}

func formatTitle(txt string) string {
	// var titleColor = "green"
	// return fmt.Sprintf(" [%s:bg:b]%s[-:-:-] ", titleColor, txt)
	return fmt.Sprintf(" [:bg:b]%s[-:-:-] ", txt)
}

func defaultBorderStyle() tcell.Style {
	fg := tcell.ColorGreen
	bg := tcell.ColorDefault
	return tcell.StyleDefault.Foreground(fg).Background(bg)
}

func setPanelDefaultStyle(p *tview.Box) {
	p.SetBorder(true)
	p.SetBorderStyle(defaultBorderStyle())
	p.SetTitleColor(tcell.ColorGreen)
	p.SetBackgroundColor(tcell.ColorDefault)
}

func setPanelFocusStyle(p *tview.Box) {
	p.SetBorderColor(tcell.ColorBlue)
	p.SetTitleColor(tcell.ColorBlue)
	p.SetBackgroundColor(tcell.ColorDarkBlue)
}

// NewMultiWindowTUI creates a new MultiWindowTUI instance
func NewMultiWindowTUI(tabs []*v1alpha1.DashboardTab, githubToken string) *MultiWindowTUI {
	return &MultiWindowTUI{
		app:           tview.NewApplication(),
		pages:         tview.NewPages(),
		tabs:          tabs,
		githubToken:   githubToken,
		refreshStopCh: make(chan struct{}),
	}
}

// SetRefreshConfig sets the configuration for auto-refresh
func (m *MultiWindowTUI) SetRefreshConfig(tg *testgrid.TestGrid, dashboards []string, minFailure, minFlake int) {
	m.testgridClient = tg
	m.dashboards = dashboards
	m.minFailure = minFailure
	m.minFlake = minFlake
}

// Run starts the TUI application
func (m *MultiWindowTUI) Run() error {
	// Create all views
	m.brokenTestsPage = m.createBrokenTestsView()
	m.mcpIssuesPage = m.createMCPIssuesView()

	// Add pages
	m.pages.AddPage("broken_tests", m.brokenTestsPage, true, true)
	m.pages.AddPage("mcp_issues", m.mcpIssuesPage, true, false)

	// Set up global key handler
	m.app.SetInputCapture(m.globalKeyHandler)

	// Start auto-refresh if configured
	if m.testgridClient != nil {
		m.startAutoRefresh()
	}

	// Cleanup on exit
	defer m.stopAutoRefresh()

	return m.app.SetRoot(m.pages, true).EnableMouse(true).Run()
}

// globalKeyHandler handles global keyboard shortcuts for navigation
func (m *MultiWindowTUI) globalKeyHandler(event *tcell.EventKey) *tcell.EventKey {
	// handle F1 for broken tests
	if event.Key() == tcell.KeyF1 {
		m.pages.SwitchToPage("broken_tests")
		return nil
	}
	// handle F2 for MCP issues
	if event.Key() == tcell.KeyF2 {
		m.pages.SwitchToPage("mcp_issues")
		return nil
	}
	// handle Ctrl-C for exit
	if event.Key() == tcell.KeyCtrlC {
		m.app.Stop()
		return nil
	}
	return event
}

// createBrokenTestsView creates the broken tests view with tabs, tests, slack, and github panels
func (m *MultiWindowTUI) createBrokenTestsView() *tview.Flex {

	// Header panel with keybindings
	headerPanel := tview.NewTextView()
	setPanelDefaultStyle(headerPanel.Box)
	headerPanel.SetTitle(formatTitle("Keybindings"))
	headerPanel.SetDynamicColors(true)
	headerText := `[white]Actions: [yellow]Ctrl-Space[white] Copy  [yellow]Ctrl-B[white] Create Issue  [yellow]F-1[white] Broken Tests  [yellow]F-2[white] MCP Issues  [yellow]Ctrl-C[white] Exit`
	headerPanel.SetText(headerText)

	// Render tab in the first row
	tabsPanel := tview.NewList().ShowSecondaryText(false)
	setPanelDefaultStyle(tabsPanel.Box)
	tabsPanel.SetTitle(formatTitle("Board - Tabs"))

	// Broken tests in the tab
	brokenPanel := tview.NewList().ShowSecondaryText(false)
	brokenPanel.SetDoneFunc(func() { m.app.SetFocus(tabsPanel) })
	setPanelDefaultStyle(brokenPanel.Box)
	brokenPanel.SetTitle(formatTitle("Tests"))

	// Slack Final issue rendering
	slackPanel := tview.NewTextArea()
	setPanelDefaultStyle(slackPanel.Box)
	slackPanel.SetTitle(formatTitle("Slack Message"))
	slackPanel.SetWrap(true).SetDisabled(true)

	// GitHub panel rendering
	githubPanel := tview.NewTextArea()
	setPanelDefaultStyle(githubPanel.Box)
	githubPanel.SetTitle(formatTitle("Github Issue"))
	githubPanel.SetWrap(true)

	// Final position bottom panel for information
	position := tview.NewTextView()
	var positionText = "[yellow]Select a test to view details"
	position.SetDynamicColors(true).SetTextAlign(tview.AlignCenter).SetText(positionText)

	// Tabs iteration for building the middle panels and actions settings
	for _, tab := range m.tabs {
		icon := "🟣"
		if tab.TabState == v1alpha1.FAILING_STATUS {
			icon = "🔴"
		}
		tabCopy := tab // Capture for closure
		tabsPanel.AddItem(fmt.Sprintf("[%s] %s", icon, strings.ReplaceAll(tab.BoardHash, "#", " - ")), "", 0, func() {
			brokenPanel.Clear()
			for _, test := range tabCopy.TestRuns {
				brokenPanel.AddItem(test.TestName, "", 0, nil)
			}
			m.app.SetFocus(brokenPanel)
			brokenPanel.SetCurrentItem(0)
			brokenPanel.SetChangedFunc(func(i int, testName string, t string, s rune) {
				position.SetText(fmt.Sprintf("[blue] selected %s test ", testName))
			})
			// Broken panel rendering the function selection
			brokenPanel.SetSelectedFunc(func(i int, testName string, t string, s rune) {
				var currentTest = tabCopy.TestRuns[i]
				m.updateSlackPanel(slackPanel, tabCopy, &currentTest, position)
				m.updateGitHubPanel(githubPanel, tabCopy, &currentTest, position)
				m.app.SetFocus(slackPanel)
			})
			position.SetText(fmt.Sprintf("[blue] selected %s board", tab.TabName))
		})
	}

	// Store panel references for navigation setup
	m.tabsPanelRef = tabsPanel
	m.brokenPanelRef = brokenPanel
	m.slackPanelRef = slackPanel
	m.githubPanelRef = githubPanel
	m.positionRef = position

	// Set up navigation keybindings for panels
	m.setupPanelNavigation()

	// Create the grid layout
	// Row sizes: header(3), tabs(10), broken(10), slack/github(flexible), position(1)
	grid := tview.NewGrid().SetRows(3, 10, 10, 0, 0, 1).
		AddItem(headerPanel, 0, 0, 1, 2, 0, 0, false).
		AddItem(tabsPanel, 1, 0, 1, 2, 0, 0, true).
		AddItem(brokenPanel, 2, 0, 1, 2, 0, 0, false).
		AddItem(slackPanel, 3, 0, 2, 1, 0, 0, false).
		AddItem(githubPanel, 3, 1, 2, 1, 0, 0, false).
		AddItem(position, 5, 0, 1, 2, 0, 0, false)
	return tview.NewFlex().SetDirection(tview.FlexRow).AddItem(grid, 0, 1, true)
}

// startAutoRefresh starts the auto-refresh ticker for broken tests
func (m *MultiWindowTUI) startAutoRefresh() {
	if m.refreshTicker != nil {
		return // Already started
	}
	m.refreshBrokenTestsAsync()
	m.refreshTicker = time.NewTicker(defaultRefreshInterval)
	go func() {
		for {
			select {
			case <-m.refreshTicker.C:
				m.refreshBrokenTestsAsync()
			case <-m.refreshStopCh:
				return
			}
		}
	}()
}

// stopAutoRefresh stops the auto-refresh ticker
func (m *MultiWindowTUI) stopAutoRefresh() {
	if m.refreshTicker != nil {
		m.refreshTicker.Stop()
		m.refreshTicker = nil
	}
	select {
	case <-m.refreshStopCh:
		// Channel already closed
	default:
		close(m.refreshStopCh)
	}
}

// refreshBrokenTestsAsync refreshes the broken tests data in a background goroutine
func (m *MultiWindowTUI) refreshBrokenTestsAsync() {
	if m.testgridClient == nil || len(m.dashboards) == 0 {
		return
	}
	go func() {
		var dashboardTabs []*v1alpha1.DashboardTab
		for _, dashboard := range m.dashboards {
			dashSummaries, err := m.testgridClient.FetchTabSummary(dashboard, v1alpha1.ERROR_STATUSES)
			if err != nil {
				m.updatePositionWithError(fmt.Errorf("error fetching dashboard %s: %v", dashboard, err))
				continue
			}
			for _, dashSummary := range dashSummaries {
				dashTab, err := m.testgridClient.FetchTabTests(&dashSummary, m.minFailure, m.minFlake)
				if err != nil {
					tabName := dashboard
					if dashSummary.DashboardTab != nil {
						tabName = dashSummary.DashboardTab.TabName
					}
					m.updatePositionWithError(fmt.Errorf("error fetching table %s: %v", tabName, err))
					continue
				}
				if len(dashTab.TestRuns) > 0 {
					dashboardTabs = append(dashboardTabs, dashTab)
				}
			}
		}
		m.updateBrokenTestsUI(dashboardTabs)
	}()
}

// updateBrokenTestsUI updates the UI with new tabs data
func (m *MultiWindowTUI) updateBrokenTestsUI(newTabs []*v1alpha1.DashboardTab) {
	m.app.QueueUpdateDraw(func() {
		// Update tabs data
		m.tabs = newTabs

		// Clear and rebuild tabs panel
		if m.tabsPanelRef != nil {
			m.tabsPanelRef.Clear()
			for _, tab := range m.tabs {
				icon := "🟣"
				if tab.TabState == v1alpha1.FAILING_STATUS {
					icon = "🔴"
				}
				tabCopy := tab // Capture for closure
				m.tabsPanelRef.AddItem(fmt.Sprintf("[%s] %s", icon, strings.ReplaceAll(tab.BoardHash, "#", " - ")), "", 0, func() {
					if m.brokenPanelRef != nil {
						m.brokenPanelRef.Clear()
						for _, test := range tabCopy.TestRuns {
							m.brokenPanelRef.AddItem(test.TestName, "", 0, nil)
						}
						m.app.SetFocus(m.brokenPanelRef)
						m.brokenPanelRef.SetCurrentItem(0)
						m.brokenPanelRef.SetChangedFunc(func(i int, testName string, t string, s rune) {
							if m.positionRef != nil {
								m.positionRef.SetText(fmt.Sprintf("[blue] selected %s test ", testName))
							}
						})
						// Broken panel rendering the function selection
						m.brokenPanelRef.SetSelectedFunc(func(i int, testName string, t string, s rune) {
							var currentTest = tabCopy.TestRuns[i]
							if m.slackPanelRef != nil && m.githubPanelRef != nil && m.positionRef != nil {
								m.updateSlackPanel(m.slackPanelRef, tabCopy, &currentTest, m.positionRef)
								m.updateGitHubPanel(m.githubPanelRef, tabCopy, &currentTest, m.positionRef)
								m.app.SetFocus(m.slackPanelRef)
							}
						})
						if m.positionRef != nil {
							m.positionRef.SetText(fmt.Sprintf("[blue] selected %s board", tab.TabName))
						}
					}
				})
			}
			// Update position message
			if m.positionRef != nil {
				m.positionRef.SetText(fmt.Sprintf("[green]Auto-refreshed: %d tabs loaded", len(m.tabs)))
			}
		}
	})
}

// updatePositionWithError updates the position panel with an error message
func (m *MultiWindowTUI) updatePositionWithError(err error) {
	if m.positionRef != nil {
		m.app.QueueUpdateDraw(func() {
			m.positionRef.SetText(fmt.Sprintf("[red]Refresh error: %v", err))
		})
	}
}

// setupPanelNavigation sets up keyboard navigation between panels
func (m *MultiWindowTUI) setupPanelNavigation() {
	// Board#Tabs panel navigation
	m.tabsPanelRef.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyDown, tcell.KeyUp:
			// Allow normal list navigation
			return event
		}
		return event
	})

	// Tests panel navigation
	m.brokenPanelRef.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			// Go back to Board#Tabs
			m.app.SetFocus(m.tabsPanelRef)
			return nil
		case tcell.KeyDown, tcell.KeyUp:
			// Allow normal list navigation
			return event
		case tcell.KeyTab:
			// Move to Slack panel
			m.app.SetFocus(m.slackPanelRef)
			return nil
		}
		return event
	})
}

// createMCPIssuesView creates the MCP issues view
func (m *MultiWindowTUI) createMCPIssuesView() *tview.Flex {
	// MCP panel rendering
	mcpPanel := tview.NewTextArea()
	setPanelDefaultStyle(mcpPanel.Box)
	mcpPanel.SetTitle(formatTitle("MCP Issues"))
	mcpPanel.SetWrap(true).SetDisabled(false)

	// Status panel
	statusPanel := tview.NewTextView()
	setPanelDefaultStyle(statusPanel.Box)
	statusPanel.SetTitle(formatTitle("Status"))
	statusPanel.SetDynamicColors(true)
	statusPanel.SetText("[yellow]Loading issues from MCP server...")

	// Help panel
	helpPanel := tview.NewTextView()
	var helpText = "[yellow]Press [blue]F-1 [yellow]for Broken Tests, [blue]F-2 [yellow]for MCP Issues, [blue]Ctrl-C [yellow]to exit"
	helpPanel.SetDynamicColors(true).SetTextAlign(tview.AlignCenter).SetText(helpText)

	// Store mcpPanel reference for async updates
	m.mcpPanelRef = mcpPanel
	m.statusPanelRef = statusPanel

	// Create layout
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(statusPanel, 3, 0, false).
		AddItem(mcpPanel, 0, 1, true).
		AddItem(helpPanel, 1, 0, false)

	m.loadGithubIssuesAsync()
	return flex
}
func initMCPConfig() (endpoint, apiKey string) {
	endpoint = os.Getenv("MCP_SERVER_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultMCPEndpoint
	}

	apiKey = os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("SIGNALHOUND_ANTHROPIC_API_KEY")
	}
	return endpoint, apiKey
}

// updateUIWithError updates UI components with the given error
func (m *MultiWindowTUI) updateUIWithError(err error) {
	m.app.QueueUpdateDraw(func() {
		errMsg := fmt.Sprintf(errorMsgFormat, err)
		if m.mcpPanelRef != nil {
			m.mcpPanelRef.SetText(errMsg, false)
		}
		if m.statusPanelRef != nil {
			m.statusPanelRef.SetText(fmt.Sprintf("[red]Error: %v", err))
		}
	})
}

// updateUIWithSuccess updates UI components with successful response
func (m *MultiWindowTUI) updateUIWithSuccess(response string) {
	m.app.QueueUpdateDraw(func() {
		if m.mcpPanelRef != nil {
			m.mcpPanelRef.SetText(response, false)
		}
		if m.statusPanelRef != nil {
			m.statusPanelRef.SetText(successMsg)
		}
	})
}

// loadGithubIssuesAsync loads GitHub issues in a background goroutine
func (m *MultiWindowTUI) loadGithubIssuesAsync() {
	mcpEndpoint, anthropicAPIKey := initMCPConfig()
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(10 * time.Second)
			client, err := intmcp.NewMCPClient(anthropicAPIKey, mcpEndpoint)
			if err != nil {
				m.updateUIWithError(err)
				return
			}
			response, err := client.LoadGithubIssues(m.tabs)
			if err != nil {
				m.updateUIWithError(err)
				return
			}
			m.updateUIWithSuccess(response)
			return
		}
	}()
}

// updateSlackPanel writes down to left panel (Slack) content.
func (m *MultiWindowTUI) updateSlackPanel(slackPanel *tview.TextArea, tab *v1alpha1.DashboardTab, currentTest *v1alpha1.TestResult, position *tview.TextView) {
	// set the item string with current test content
	item := fmt.Sprintf("%s %s on [%s](%s): `%s` [Prow](%s), [Triage](%s), last failure on %s\n",
		tab.StateIcon, cases.Title(language.English).String(tab.TabState), tab.BoardHash, tab.TabURL,
		currentTest.TestName, currentTest.ProwJobURL, currentTest.TriageURL, timeClean(currentTest.LatestTimestamp),
	)

	// set input capture, ctrl-space for clipboard copy
	slackPanel.SetText(item, true)
	// Set up navigation and actions for Slack panel
	slackPanel.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlSpace {
			position.SetText("[blue]COPIED [yellow]SLACK [blue]TO THE CLIPBOARD!")
			if err := CopyToClipboard(slackPanel.GetText()); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				return event
			}
			setPanelFocusStyle(slackPanel.Box)
			go func() {
				time.Sleep(1 * time.Second)
				m.app.QueueUpdateDraw(func() {
					setPanelDefaultStyle(slackPanel.Box)
				})
			}()
			return nil
		}
		// Navigation
		switch event.Key() {
		case tcell.KeyRight:
			m.app.SetFocus(m.githubPanelRef)
			return nil
		case tcell.KeyLeft, tcell.KeyUp, tcell.KeyEscape:
			m.app.SetFocus(m.brokenPanelRef)
			return nil
		}
		return event
	})
}

// updateGitHubPanel writes down to the right panel (GitHub) content.
func (m *MultiWindowTUI) updateGitHubPanel(githubPanel *tview.TextArea, tab *v1alpha1.DashboardTab, currentTest *v1alpha1.TestResult, position *tview.TextView) {
	// create the filled-out issue template object
	splitBoard := strings.Split(tab.BoardHash, "#")
	issue := &IssueTemplate{
		BoardName:    splitBoard[0],
		TabName:      splitBoard[1],
		TestName:     currentTest.TestName,
		TestGridURL:  tab.TabURL,
		TriageURL:    currentTest.TriageURL,
		ProwURL:      currentTest.ProwJobURL,
		ErrMessage:   currentTest.ErrorMessage,
		FirstFailure: timeClean(currentTest.FirstTimestamp),
		LastFailure:  timeClean(currentTest.LatestTimestamp),
	}

	// pick the correct template by failure status
	templateFile, prefixTitle := "template/flake.tmpl", "Flaking Test"
	if tab.TabState == v1alpha1.FAILING_STATUS {
		templateFile, prefixTitle = "template/failure.tmpl", "Failing Test"
	}
	template, err := renderTemplate(issue, templateFile)
	if err != nil {
		position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
		return
	}
	issueBody := template.String()
	issueTitle := fmt.Sprintf("[%v] %v", prefixTitle, currentTest.TestName)
	githubPanel.SetText(issueBody, false)

	// set input capture, ctrl-space for clipboard copy, ctrl-b for
	// automatic GitHub draft issue creation.
	githubPanel.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlSpace {
			position.SetText("[blue]COPIED [yellow]ISSUE [blue]TO THE CLIPBOARD!")
			if err := CopyToClipboard(githubPanel.GetText()); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				return event
			}
			setPanelFocusStyle(githubPanel.Box)
			go func() {
				time.Sleep(1 * time.Second)
				m.app.QueueUpdateDraw(func() {
					setPanelDefaultStyle(githubPanel.Box)
				})
			}()
			return nil
		}
		if event.Key() == tcell.KeyCtrlB {
			gh := github.NewProjectManager(context.Background(), m.githubToken)
			if err := gh.CreateDraftIssue(issueTitle, issueBody, tab.BoardHash); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				return event
			}
			position.SetText("[blue]Created [yellow]DRAFT ISSUE [blue] on GitHub Project!")
			setPanelFocusStyle(githubPanel.Box)
			go func() {
				time.Sleep(1 * time.Second)
				m.app.QueueUpdateDraw(func() {
					setPanelDefaultStyle(githubPanel.Box)
				})
			}()
			return nil
		}
		// Navigation
		switch event.Key() {
		case tcell.KeyLeft:
			m.app.SetFocus(m.slackPanelRef)
			return nil
		case tcell.KeyUp, tcell.KeyEscape:
			m.app.SetFocus(m.brokenPanelRef)
			return nil
		}
		return event
	})
}

// timeClean returns the string representation of the timestamp.
func timeClean(ts int64) string {
	return time.Unix(ts/1000, 0).UTC().Format(time.RFC1123)
}

// CopyToClipboard pipes the panel content to clip.exe WSL.
func CopyToClipboard(text string) error {
	var cmd *exec.Cmd
	// Detect the operating system and use appropriate clipboard command
	switch runtime.GOOS {
	case "windows":
		// Native Windows
		cmd = exec.Command("clip.exe")
		cmd.Stdin = strings.NewReader(text)
	case "darwin":
		// macOS
		cmd = exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
	case "linux":
		// Linux - need to check for available clipboard manager
		// Try different clipboard managers in order of preference

		// Check if running under WSL
		if isWSL() {
			// WSL environment - use clip.exe
			cmd = exec.Command("clip.exe")
			cmd.Stdin = strings.NewReader(text)
		} else if isWayland() {
			// Wayland
			cmd = exec.Command("wl-copy")
			cmd.Stdin = strings.NewReader(text)
		} else {
			// X11
			cmd = exec.Command("xclip", "-selection", "clipboard")
			cmd.Stdin = strings.NewReader(text)
		}

	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
	return cmd.Run()
}
