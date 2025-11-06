package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/knabben/signalhound/api/v1alpha1"
	"github.com/knabben/signalhound/internal/github"
	"github.com/knabben/signalhound/internal/logger"
	"github.com/rivo/tview"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	pagesName   = "SignalHound"
	app         *tview.Application // The tview application.
	pages       *tview.Pages       // The application pages.
	brokenPanel = tview.NewList()
	slackPanel  = tview.NewTextArea()
	githubPanel = tview.NewTextArea()
	position    = tview.NewTextView()
)

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

// RenderVisual loads the entire grid and componnents in the app.
// this is a blocking functions.
func RenderVisual(tabs []*v1alpha1.DashboardTab, githubToken string) error {
	app = tview.NewApplication()

	// Render tab in the first row
	tabsPanel := tview.NewList().ShowSecondaryText(false)
	setPanelDefaultStyle(tabsPanel.Box)
	tabsPanel.SetTitle(formatTitle("Board#Tabs"))

	// Broken tests in the tab
	brokenPanel.ShowSecondaryText(false).SetDoneFunc(func() { app.SetFocus(tabsPanel) })
	setPanelDefaultStyle(brokenPanel.Box)
	brokenPanel.SetTitle(formatTitle("Tests"))

	// Slack Final issue rendering
	setPanelDefaultStyle(slackPanel.Box)
	slackPanel.SetTitle(formatTitle("Slack Message"))
	slackPanel.SetWrap(true).SetDisabled(true)

	// GitHub panel rendering
	setPanelDefaultStyle(githubPanel.Box)
	githubPanel.SetTitle(formatTitle("Github Issue"))
	githubPanel.SetWrap(true)

	// Final position bottom panel for information
	var positionText = "[yellow]Select a content window and press [blue]Ctrl-Space [yellow]to COPY or press [blue]Ctrl-C [yellow]to exit"
	position.SetDynamicColors(true).SetTextAlign(tview.AlignCenter).SetText(positionText)

	// Create the grid layout
	grid := tview.NewGrid().SetRows(10, 10, 0, 0, 1).
		AddItem(tabsPanel, 0, 0, 1, 2, 0, 0, true).
		AddItem(brokenPanel, 1, 0, 1, 2, 0, 0, false).
		AddItem(position, 4, 0, 1, 2, 0, 0, false)

	// Adding middle panel and split across rows and columns
	grid.AddItem(slackPanel, 2, 0, 2, 1, 0, 0, false).
		AddItem(githubPanel, 2, 1, 2, 1, 0, 0, false)

	// Tabs iteration for building the middle panels and actions settings
	for _, tab := range tabs {
		icon := "🟣"
		if tab.TabState == v1alpha1.FAILING_STATUS {
			icon = "🔴"
		}
		tabsPanel.AddItem(fmt.Sprintf("[%s] %s", icon, strings.ReplaceAll(tab.BoardHash, "#", " - ")), "", 0, func() {
			brokenPanel.Clear()
			for _, test := range tab.TestRuns {
				brokenPanel.AddItem(test.TestName, "", 0, nil)
			}
			app.SetFocus(brokenPanel)
			brokenPanel.SetCurrentItem(0)
			brokenPanel.SetChangedFunc(func(i int, testName string, t string, s rune) {
				position.SetText(positionText)
			})
			// Broken panel rendering the function selection
			brokenPanel.SetSelectedFunc(func(i int, testName string, t string, s rune) {
				var currentTest = tab.TestRuns[i]
				updateSlackPanel(tab, &currentTest)
				updateGitHubPanel(tab, &currentTest, githubToken)
				app.SetFocus(slackPanel)
			})
		})
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

	// set input capture, ctrl-space for clipboard copy, esc to cancel panel selection.
	slackPanel.SetText(item, true)
	slackPanel.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlSpace {
			position.SetText("[blue]COPIED [yellow]SLACK [blue]TO THE CLIPBOARD!")
			if err := CopyToClipboard(slackPanel.GetText()); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				logger.HandleError(err)
				return event
			}
			setPanelFocusStyle(slackPanel.Box)
			go func() {
				time.Sleep(1 * time.Second)
				app.QueueUpdateDraw(func() {
					app.SetFocus(brokenPanel)
					setPanelDefaultStyle(slackPanel.Box)
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
	// create the filled out issue template object
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
		logger.HandleError(err)
		return
	}
	issueTemplate := template.String()
	issueTitle := fmt.Sprintf("[%v] %v", prefixTitle, currentTest.TestName)
	githubPanel.SetText(issueTemplate, false)

	// set input capture, ctrl-space for clipboard copy, ctrl-b for
	// automatic GitHub draft issue creation.
	githubPanel.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlSpace {
			position.SetText("[blue]COPIED [yellow]ISSUE [blue]TO THE CLIPBOARD!")
			if err := CopyToClipboard(githubPanel.GetText()); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				logger.HandleError(err)
				return event
			}
			setPanelFocusStyle(githubPanel.Box)
			go func() {
				time.Sleep(1 * time.Second)
				app.QueueUpdateDraw(func() {
					app.SetFocus(brokenPanel)
					setPanelDefaultStyle(githubPanel.Box)
				})
			}()
		}
		if event.Key() == tcell.KeyCtrlB {
			gh := github.NewGithub(context.Background(), token)
			if err := gh.CreateDraftIssue(issueTitle, issueTemplate); err != nil {
				position.SetText(fmt.Sprintf("[red]error: %v", err.Error()))
				logger.HandleError(err)
				return event
			}
			position.SetText("[blue]Created [yellow]DRAFT ISSUE [blue] on GitHub Project!")
			setPanelFocusStyle(githubPanel.Box)
			go func() {
				time.Sleep(1 * time.Second)
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
		err := fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
		logger.HandleError(err)
		return err
	}

	return cmd.Run()

}
