package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/session"
)

const buildLogLimit = 256 * 1024

func (m *Model) startBuild() (tea.Model, tea.Cmd) {
	if m.draft == nil {
		m.notice = "draft is unavailable"
		return m, nil
	}
	if !m.designBuildable || m.designDir == "" {
		m.notice = "Design must pass draft check before build"
		return m, nil
	}
	m.screen = screenBuild
	m.running = true
	m.job = jobDraftBuild
	m.buildState = buildRunning
	m.buildLog = ""
	m.buildAnswer = ""
	m.buildSession = ""
	m.notice = ""
	m.viewport.GotoBottom()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.draftEvents = m.draft.Build(ctx, draftexec.BuildRequest{Dir: m.designDir, Model: m.modelName})
	m.syncContent()
	return m, tea.Batch(waitDraftEvent(m.draftEvents), tick())
}

func (m *Model) updateBuild(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		switch key {
		case "ctrl+c", "esc":
			m.interrupt()
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.screen = screenDesignReview
		m.viewport.GotoTop()
		m.notice = "Build evidence remains with the project"
		m.syncContent()
		return m, nil
	case "r":
		return m.startBuild()
	case "p":
		if m.buildState != buildPassed {
			m.notice = "The build check must pass before evaluation"
			return m, nil
		}
		return m.startProve()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateBuildProcess(event draftexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case draftexec.Stdout:
			m.buildAnswer += safeText(event.Text)
		case draftexec.Stderr:
			m.appendBuildLog(event.Text)
		}
		m.syncContent()
		return m, waitDraftEvent(m.draftEvents)
	}

	m.running = false
	m.cancel = nil
	m.job = 0
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.buildState = buildInterrupted
		m.notice = "Build interrupted · run it again to resume from the worktree"
	case event.Err == nil && event.ExitCode == 0:
		m.buildState = buildPassed
		m.notice = ""
	case event.ExitCode == 2:
		m.buildState = buildNotDone
		m.notice = "Not done · the check still fails or a bound was reached"
	default:
		m.buildState = buildFailed
		m.notice = filterFailure("draft build", event.ExitCode, event.Err, m.buildLog)
	}
	if saved, err := session.Discover(filepath.Join(m.designDir, ".draft", "build")); err == nil && len(saved) > 0 {
		m.buildSession = saved[0].Path
	}
	m.syncContent()
	return m, nil
}

func (m *Model) appendBuildLog(s string) {
	m.buildLog += safeText(s)
	runes := []rune(m.buildLog)
	if len(runes) <= buildLogLimit {
		return
	}
	marker := []rune("[earlier typescript omitted from this view; the ask session is authoritative]\n")
	keep := buildLogLimit - len(marker)
	m.buildLog = string(marker) + string(runes[len(runes)-keep:])
}

func (m *Model) renderBuild(width int) string {
	t := makeTheme(m.dark)
	verdict, style := m.buildVerdict(t)
	rows := []string{
		t.hero.Render(filepath.Base(m.designDir)) + "  " + style.Render(verdict),
		t.faint.Render("draft build " + m.designDir),
		"",
	}
	log := strings.TrimSpace(m.buildLog)
	if log == "" && m.running {
		log = spinnerFrame(m.spinner) + "  waiting for draft/ply"
	}
	if log != "" {
		rows = append(rows, t.sessionLabel.Render("TYPESCRIPT"), t.document.Width(max(16, width-5)).Render(log))
	}
	if answer := strings.TrimSpace(m.buildAnswer); answer != "" {
		rows = append(rows, "", t.askLabel.Render("ANSWER"), t.document.Width(max(16, width-5)).Render(answer))
	}
	if m.buildSession != "" {
		rows = append(rows, "", t.faint.Render("evidence  "+m.buildSession))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) buildVerdict(t theme) (string, lipgloss.Style) {
	switch m.buildState {
	case buildRunning:
		return "… BUILDING", t.sessionLabel
	case buildPassed:
		return "✓ CHECK PASSED", t.success
	case buildNotDone:
		return "○ NOT DONE", t.warning
	case buildInterrupted:
		return "○ INTERRUPTED", t.warning
	case buildFailed:
		return "! BUILD FAILED", t.danger
	default:
		return "○ NOT STARTED", t.muted
	}
}
