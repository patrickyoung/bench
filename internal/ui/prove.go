package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/patrickyoung/bench/internal/draftexec"
)

const proveOutputLimit = 256 * 1024

func (m Model) startProve() (tea.Model, tea.Cmd) {
	if m.draft == nil {
		m.notice = "draft is unavailable"
		return m, nil
	}
	if m.buildState != buildPassed {
		m.notice = "The build check must pass before evaluation"
		return m, nil
	}
	m.screen = screenProve
	m.running = true
	m.job = jobDraftProve
	m.proveState = proveRunning
	m.proveLog = ""
	m.proveFindings = ""
	m.notice = ""
	m.viewport.GotoBottom()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.draftEvents = m.draft.Prove(ctx, m.designDir)
	m.syncContent()
	return m, tea.Batch(waitDraftEvent(m.draftEvents), tick())
}

func (m Model) updateProve(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
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
		m.screen = screenBuild
		m.viewport.GotoBottom()
		m.notice = "Evaluation is rerunnable with draft prove"
		m.syncContent()
		return m, nil
	case "r":
		return m.startProve()
	case "l":
		if m.proveState != provePassed {
			m.notice = "Evaluation must be proven before learning"
			return m, nil
		}
		return m.openLearn()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateProveProcess(event draftexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case draftexec.Stdout:
			m.proveFindings = appendVisibleOutput(m.proveFindings, event.Text, proveOutputLimit,
				"[earlier survivors omitted from this view; rerun draft prove for the full report]\n")
		case draftexec.Stderr:
			m.proveLog = appendVisibleOutput(m.proveLog, event.Text, proveOutputLimit,
				"[earlier measurement output omitted from this view; rerun draft prove for the full report]\n")
		}
		m.syncContent()
		return m, waitDraftEvent(m.draftEvents)
	}
	m.running = false
	m.cancel = nil
	m.job = 0
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.proveState = proveInterrupted
		m.notice = "Evaluation interrupted; draft restores any active mutation"
	case event.Err == nil && event.ExitCode == 0:
		m.proveState = provePassed
		m.notice = ""
	case event.ExitCode == 1:
		m.proveState = proveGaps
		m.notice = "Gaps found · strengthen the check, rebuild, and prove again"
	default:
		m.proveState = proveFailed
		m.notice = filterFailure("draft prove", event.ExitCode, event.Err, m.proveLog)
	}
	m.syncContent()
	return m, nil
}

func appendVisibleOutput(existing, addition string, limit int, marker string) string {
	runes := []rune(existing + safeText(addition))
	if len(runes) <= limit {
		return string(runes)
	}
	markerRunes := []rune(marker)
	keep := limit - len(markerRunes)
	if keep < 0 {
		return string(markerRunes[:limit])
	}
	return string(markerRunes) + string(runes[len(runes)-keep:])
}

func (m Model) renderProve(width int) string {
	t := makeTheme(m.dark)
	verdict, style := m.proveVerdict(t)
	rows := []string{
		t.hero.Render(filepath.Base(m.designDir)) + "  " + style.Render(verdict),
		t.faint.Render("draft prove " + m.designDir),
		"",
	}
	if log := strings.TrimSpace(m.proveLog); log != "" {
		rows = append(rows, t.sessionLabel.Render("MEASUREMENT"), t.document.Width(max(16, width-5)).Render(log))
	} else if m.running {
		rows = append(rows, t.working.Render(spinnerFrame(m.spinner)+"  mutating and running the check"))
	}
	if findings := strings.TrimSpace(m.proveFindings); findings != "" {
		rows = append(rows, "", t.warning.Render("SURVIVORS"), t.document.Width(max(16, width-5)).Render(findings))
	}
	return strings.Join(rows, "\n")
}

func (m Model) proveVerdict(t theme) (string, lipgloss.Style) {
	switch m.proveState {
	case proveRunning:
		return "… EVALUATING", t.sessionLabel
	case provePassed:
		return "✓ CHECK PROVEN", t.success
	case proveGaps:
		return "○ GAPS FOUND", t.warning
	case proveInterrupted:
		return "○ INTERRUPTED", t.warning
	case proveFailed:
		return "! PROVE FAILED", t.danger
	default:
		return "○ NOT RUN", t.muted
	}
}
