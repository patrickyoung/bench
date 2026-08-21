package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/patrickyoung/bench/internal/honeexec"
)

const learnOutputLimit = 256 * 1024

func (m *Model) openLearn() (tea.Model, tea.Cmd) {
	if m.hone == nil {
		m.notice = "hone is unavailable"
		return m, nil
	}
	if m.buildSession == "" {
		m.notice = "No replayable build session is available to learn from"
		return m, nil
	}
	m.learnReturn = m.screen
	m.screen = screenLearn
	m.learnState = learnIdle
	m.learnLog = ""
	m.learnOutput = ""
	if strings.TrimSpace(m.skill.Value()) == "" {
		m.skill.SetValue(filepath.Base(m.designDir))
	}
	m.notice = "Choose the brief skill that should receive verified lessons"
	m.viewport.GotoTop()
	m.syncContent()
	cmd := m.skill.Focus()
	return m, cmd
}

func (m *Model) startLearn() (tea.Model, tea.Cmd) {
	if m.hone == nil {
		m.notice = "hone is unavailable"
		return m, nil
	}
	skill := strings.TrimSpace(m.skill.Value())
	if skill == "" {
		m.notice = "Name a skill before learning"
		return m, nil
	}
	if m.buildSession == "" {
		m.notice = "No replayable build session is available to learn from"
		return m, nil
	}
	m.running = true
	m.job = jobHone
	m.learnState = learnRunning
	m.learnLog = ""
	m.learnOutput = ""
	m.notice = ""
	m.skill.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.honeEvents = m.hone.Learn(ctx, honeexec.Request{Session: m.buildSession, Skill: skill})
	m.syncContent()
	return m, tea.Batch(waitHoneEvent(m.honeEvents), tick())
}

func (m *Model) updateLearn(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
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
		m.screen = m.learnReturn
		if m.screen == screenLearn {
			m.screen = screenProve
		}
		m.skill.Blur()
		m.viewport.GotoBottom()
		m.notice = "Learning evidence remains in the brief skill"
		m.syncContent()
		return m, nil
	case "ctrl+s", "ctrl+enter":
		return m.startLearn()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.skill, cmd = m.skill.Update(msg)
	m.syncContent()
	return m, cmd
}

func (m *Model) updateLearnProcess(event honeexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case honeexec.Stdout:
			m.learnOutput = appendVisibleOutput(m.learnOutput, event.Text, learnOutputLimit,
				"[earlier lesson output omitted from this view; the skill file is authoritative]\n")
		case honeexec.Stderr:
			m.learnLog = appendVisibleOutput(m.learnLog, event.Text, learnOutputLimit,
				"[earlier hone provenance omitted from this view; rerun hone for the full report]\n")
		}
		m.syncContent()
		return m, waitHoneEvent(m.honeEvents)
	}
	m.running = false
	m.cancel = nil
	m.job = 0
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.learnState = learnInterrupted
		m.notice = "Learning interrupted; hone admits complete lessons only"
	case event.Err == nil && event.ExitCode == 0:
		m.learnState = learned
		m.notice = ""
	case event.ExitCode == 1:
		m.learnState = learnNothing
		m.notice = "Nothing learned · hone found no new verified recovery"
	default:
		m.learnState = learnFailed
		m.notice = filterFailure("hone", event.ExitCode, event.Err, m.learnLog)
	}
	m.syncContent()
	cmd := m.skill.Focus()
	return m, cmd
}

func waitHoneEvent(events <-chan honeexec.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return honeProcessEvent{Done: true, ExitCode: 2, Err: errors.New("hone event stream closed")}
		}
		return honeProcessEvent(event)
	}
}

func (m *Model) renderLearn(width int) string {
	t := makeTheme(m.dark)
	verdict, style := m.learnVerdict(t)
	rows := []string{
		t.hero.Render(filepath.Base(m.designDir)) + "  " + style.Render(verdict),
		t.faint.Render("evidence  " + m.buildSession),
		"",
		t.sessionLabel.Render("BRIEF SKILL"),
		t.input.Width(max(16, width-6)).Render(m.skill.View()),
	}
	if log := strings.TrimSpace(m.learnLog); log != "" {
		rows = append(rows, "", t.sessionLabel.Render("HONE PROVENANCE"), t.document.Width(max(16, width-5)).Render(log))
	} else if m.running {
		rows = append(rows, "", t.working.Render(spinnerFrame(m.spinner)+"  verifying the build session"))
	}
	if output := strings.TrimSpace(m.learnOutput); output != "" {
		rows = append(rows, "", t.askLabel.Render("LESSONS"), t.document.Width(max(16, width-5)).Render(output))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) learnVerdict(t theme) (string, lipgloss.Style) {
	switch m.learnState {
	case learnRunning:
		return "… VERIFYING", t.sessionLabel
	case learned:
		return "✓ LESSON ADMITTED", t.success
	case learnNothing:
		return "○ NOTHING TO LEARN", t.warning
	case learnInterrupted:
		return "○ INTERRUPTED", t.warning
	case learnFailed:
		return "! HONE FAILED", t.danger
	default:
		return "○ READY", t.muted
	}
}
