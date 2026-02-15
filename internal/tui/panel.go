package tui

import (
	"context"
	"fmt"
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
)

const (
	defaultPositionText = "[green]Select a content Windows and press [blue]yy [green]to COPY or press [blue]Ctrl-C [green]to exit"
	yankTimeout         = 750 * time.Millisecond
)

var (
	pagesName         = "SignalHound"
	app               *tview.Application // The tview application.
	pages             *tview.Pages       // The application pages.
	tabsPanel         *tview.List        // The tabs panel (needs to be accessible for updates)
	brokenPanel       = tview.NewList()
	slackPanel        = tview.NewTextArea()
	githubPanel       = tview.NewTextArea()
	position          = tview.NewTextView()
	currentTabs       []*v1alpha1.DashboardTab // Store current tabs for refresh
	githubToken       string                   // Store token for refresh
	selectedBoardHash string                   // Store selected BoardHash for refresh preservation
	selectedTestName  string                   // Store selected test name for refresh preservation
	lastSlackYPress   time.Time                // Track "yy" clipboard shortcut in Slack panel
	lastGithubYPress  time.Time                // Track "yy" clipboard shortcut in GitHub panel
)

func isYankShortcut(event *tcell.EventKey, lastPress *time.Time) bool {
	if event.Key() != tcell.KeyRune || (event.Rune() != 'y' && event.Rune() != 'Y') {
		*lastPress = time.Time{}
		return false
	}

	now := time.Now()
	if !lastPress.IsZero() && now.Sub(*lastPress) <= yankTimeout {
		*lastPress = time.Time{}
		return true
	}

	*lastPress = now
	return false
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
	app.SetFocus(p)
}

// updateTabsPanel updates the tabs panel with new data while preserving selection if possible.
func updateTabsPanel(tabs []*v1alpha1.DashboardTab) {
	if tabsPanel == nil {
		return
	}

	// Store current selection before clearing
	if tabsPanel.GetItemCount() > 0 {
		currentIndex := tabsPanel.GetCurrentItem()
		if currentIndex >= 0 && currentIndex < len(currentTabs) {
			selectedBoardHash = currentTabs[currentIndex].BoardHash
			// Store selected test name if brokenPanel has items
			if brokenPanel.GetItemCount() > 0 {
				testIndex := brokenPanel.GetCurrentItem()
				if testIndex >= 0 && testIndex < brokenPanel.GetItemCount() {
					_, selectedTestName = brokenPanel.GetItemText(testIndex)
				}
			}
		}
	}

	// Clear and rebuild the tabs panel
	tabsPanel.Clear()
	// Map to store tab selection callbacks by BoardHash for restoration
	tabCallbacks := make(map[string]func())

	for _, tab := range tabs {
		icon := "🟣"
		if tab.TabState == v1alpha1.FAILING_STATUS {
			icon = "🔴"
		}
		tabText := fmt.Sprintf("[%s] %s", icon, strings.ReplaceAll(tab.BoardHash, "#", " - "))

		// Create selection callback for this tab
		tabCallback := func(tab *v1alpha1.DashboardTab) func() {
			return func() {
				// Store the selected BoardHash when user manually selects a tab
				selectedBoardHash = tab.BoardHash
				selectedTestName = "" // Clear test selection when tab changes

				brokenPanel.Clear()
				for _, test := range tab.TestRuns {
					brokenPanel.AddItem(tview.Escape(test.TestName), "", 0, nil)
				}
				app.SetFocus(brokenPanel)
				brokenPanel.SetCurrentItem(0)
				brokenPanel.SetChangedFunc(func(i int, testName string, secondaryText string, shortcut rune) {
					position.SetText(defaultPositionText)
					// Store the selected test name when user navigates tests
					if i >= 0 && i < brokenPanel.GetItemCount() {
						_, selectedTestName = brokenPanel.GetItemText(i)
					}
				})
				// Broken panel rendering the function selection
				brokenPanel.SetSelectedFunc(func(i int, testName string, secondaryText string, shortcut rune) {
					// Store the selected test name
					selectedTestName = testName
					var currentTest = tab.TestRuns[i]
					updateSlackPanel(tab, &currentTest)
					updateGitHubPanel(tab, &currentTest, githubToken)
					app.SetFocus(slackPanel)
				})
			}
		}(tab)

		tabCallbacks[tab.BoardHash] = tabCallback
		tabsPanel.AddItem(tabText, "", 0, tabCallback)
	}

	// Update stored tabs
	currentTabs = tabs

	// Try to restore selection by BoardHash
	if selectedBoardHash != "" {
		for i, tab := range tabs {
			if tab.BoardHash == selectedBoardHash {
				tabsPanel.SetCurrentItem(i)
				// Save test selection before callback clears it
				savedTestName := selectedTestName
				// Trigger the selection callback to restore brokenPanel
				if callback, exists := tabCallbacks[selectedBoardHash]; exists {
					callback()
					// Restore test selection if it exists
					if savedTestName != "" {
						for j := 0; j < brokenPanel.GetItemCount(); j++ {
							testName, _ := brokenPanel.GetItemText(j)
							if testName == savedTestName {
								brokenPanel.SetCurrentItem(j)
								selectedTestName = savedTestName // Restore the stored value
								break
							}
						}
					}
				}
				break
			}
		}
	}
}

// RenderVisual loads the entire grid and componnents in the app.
// this is a blocking functions.
func RenderVisual(tabs []*v1alpha1.DashboardTab, token string, refreshInterval time.Duration, refreshFunc func() ([]*v1alpha1.DashboardTab, error)) error {
	app = tview.NewApplication()
	githubToken = token
	currentTabs = tabs

	// Render tab in the first row
	tabsPanel = tview.NewList().ShowSecondaryText(false)
	setPanelDefaultStyle(tabsPanel.Box)
	tabsPanel.SetSelectedBackgroundColor(tcell.ColorBlue)
	tabsPanel.SetHighlightFullLine(true)
	tabsPanel.SetMainTextStyle(tcell.StyleDefault)
	tabsPanel.SetTitle(formatTitle("Board#Tabs"))

	// Broken tests in the tab
	brokenPanel.ShowSecondaryText(false).SetDoneFunc(func() { app.SetFocus(tabsPanel) })
	setPanelDefaultStyle(brokenPanel.Box)
	brokenPanel.SetTitle(formatTitle("Tests"))
	brokenPanel.SetSelectedBackgroundColor(tcell.ColorBlue)
	brokenPanel.SetHighlightFullLine(true)
	brokenPanel.SetMainTextStyle(tcell.StyleDefault)

	// Slack Final issue rendering
	setPanelDefaultStyle(slackPanel.Box)
	slackPanel.SetTitle(formatTitle("Slack Message"))
	slackPanel.SetWrap(true).SetDisabled(true)
	slackPanel.SetTextStyle(tcell.StyleDefault)

	// GitHub panel rendering
	setPanelDefaultStyle(githubPanel.Box)
	githubPanel.SetTitle(formatTitle("GitHub Issue"))
	githubPanel.SetWrap(true).SetDisabled(true)
	githubPanel.SetTextStyle(tcell.StyleDefault)

	// Final position bottom panel for information
	position.SetDynamicColors(true).SetTextAlign(tview.AlignCenter).SetText(defaultPositionText).SetTextStyle(tcell.StyleDefault)

	// Create the grid layout
	grid := tview.NewGrid().SetRows(10, 10, 0, 0, 1).
		AddItem(tabsPanel, 0, 0, 1, 2, 0, 0, true).
		AddItem(brokenPanel, 1, 0, 1, 2, 0, 0, false).
		AddItem(position, 4, 0, 1, 2, 0, 0, false)

	// Adding middle panel and split across rows and columns
	grid.AddItem(slackPanel, 2, 0, 2, 1, 0, 0, false).
		AddItem(githubPanel, 2, 1, 2, 1, 0, 0, false)

	// Initial tabs setup
	updateTabsPanel(tabs)

	// Set up periodic refresh if interval is configured and refresh function is provided
	if refreshInterval > 0 && refreshFunc != nil {
		go func() {
			ticker := time.NewTicker(refreshInterval)
			defer ticker.Stop()
			for range ticker.C {
				newTabs, err := refreshFunc()
				if err != nil {
					app.QueueUpdateDraw(func() {
						position.SetText(fmt.Sprintf("[red]Refresh error: %v", err))
					})
					continue
				}
				app.QueueUpdateDraw(func() {
					updateTabsPanel(newTabs)
					position.SetText(fmt.Sprintf("[green]Refreshed at %s", time.Now().Format("15:04:05")))
					// Clear refresh message after 1 seconds
					go func() {
						time.Sleep(1 * time.Second)
						app.QueueUpdateDraw(func() {
							position.SetText(defaultPositionText)
						})
					}()
				})
			}
		}()
	}

	// Render the final page.
	pages = tview.NewPages().AddPage(pagesName, grid, true, true)
	return app.SetRoot(pages, true).EnableMouse(true).Run()
}

// updateSlackPanel writes down to left panel (Slack) content.
func updateSlackPanel(tab *v1alpha1.DashboardTab, currentTest *v1alpha1.TestResult) {
	// set the item string with current test content
	item := fmt.Sprintf("%s %s on [%s](%s): `%s` [Prow](%s), [Triage](%s), last failure on %s\n",
		tab.StateIcon, cases.Title(language.English).String(tab.TabState), tab.BoardHash, tab.TabURL,
		currentTest.TestName, currentTest.ProwJobURL, currentTest.TriageURL, timeClean(currentTest.LatestTimestamp),
	)

	// set input capture, "yy" for clipboard copy, esc to cancel panel selection.
	slackPanel.SetText(item, true)
	slackPanel.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if isYankShortcut(event, &lastSlackYPress) {
			position.SetText("[blue]COPIED [yellow]SLACK [blue]TO THE CLIPBOARD!")
			if err := CopyToClipboard(slackPanel.GetText()); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				return event
			}
			setPanelFocusStyle(slackPanel.Box)
			slackPanel.SetTextStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite))
			go func() {
				time.Sleep(1 * time.Second)
				app.QueueUpdateDraw(func() {
					app.SetFocus(brokenPanel)
					setPanelDefaultStyle(slackPanel.Box)
					slackPanel.SetTextStyle(tcell.StyleDefault)
				})
			}()
		}
		if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyUp {
			slackPanel.SetText("", false)
			githubPanel.SetText("", false)
			app.SetFocus(brokenPanel)
		}
		if event.Key() == tcell.KeyRight {
			app.SetFocus(githubPanel)
		}
		return event
	})
}

// updateGitHubPanel writes down to the right panel (GitHub) content.
func updateGitHubPanel(tab *v1alpha1.DashboardTab, currentTest *v1alpha1.TestResult, token string) {
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

	// set input capture, "yy" for clipboard copy, ctrl-b for
	// automatic GitHub draft issue creation.
	githubPanel.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if isYankShortcut(event, &lastGithubYPress) {
			position.SetText("[blue]COPIED [yellow]ISSUE [blue]TO THE CLIPBOARD!")
			if err := CopyToClipboard(githubPanel.GetText()); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				return event
			}
			setPanelFocusStyle(githubPanel.Box)
			githubPanel.SetTextStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite))
			go func() {
				time.Sleep(1 * time.Second)
				app.QueueUpdateDraw(func() {
					app.SetFocus(brokenPanel)
					setPanelDefaultStyle(githubPanel.Box)
					githubPanel.SetTextStyle(tcell.StyleDefault)
				})
			}()
		}
		if event.Key() == tcell.KeyCtrlB {
			gh := github.NewProjectManager(context.Background(), token)
			if err := gh.CreateDraftIssue(issueTitle, issueBody, tab.BoardHash); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				return event
			}
			position.SetText("[blue]Created [yellow]DRAFT ISSUE [blue] on GitHub Project!")
			setPanelFocusStyle(githubPanel.Box)
			go func() {
				app.QueueUpdateDraw(func() {
					app.SetFocus(brokenPanel)
					setPanelDefaultStyle(githubPanel.Box)
				})
			}()
		}
		if event.Key() == tcell.KeyEscape {
			slackPanel.SetText("", false)
			githubPanel.SetText("", false)
			app.SetFocus(brokenPanel)
		}
		if event.Key() == tcell.KeyLeft {
			app.SetFocus(slackPanel)
		}
		if event.Key() == tcell.KeyRight {
			app.SetFocus(slackPanel)
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
		cmd = exec.Command("cmd", "/c", "echo "+text+" | clip")
		// Alternative: cmd = exec.Command("powershell", "-command", "Set-Clipboard", "-Value", text)
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
