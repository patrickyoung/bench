package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/patrickyoung/bench/internal/agentexec"
)

const (
	agentOutputLimit = 256 * 1024
	agentTaskLimit   = 16 * 1024
)

func (m *Model) startAgentCommand(command string) (tea.Model, tea.Cmd) {
	if m.agent == nil {
		m.notice = "agent is unavailable"
		return m, nil
	}
	if m.agentHome == "" {
		m.notice = "No agent home is open"
		return m, nil
	}
	args := []string{}
	input := ""
	switch command {
	case "show", "check", "proposals":
		args = []string{command, m.agentHome}
	case "run", "tick":
		args = []string{command}
		if model := strings.TrimSpace(m.modelName); model != "" {
			args = append(args, "-m", model)
		}
		args = append(args, m.agentHome)
	case "history":
		args = []string{"history", m.agentHome}
	case "history-check":
		args = []string{"history", m.agentHome, "check"}
	case "specialist":
		args = []string{"specialist", m.agentHome, m.agentChildName}
		if model := strings.TrimSpace(m.modelName); model != "" {
			args = append(args, "-m", model)
		}
		input = m.agentChildTask
	default:
		m.notice = "Unknown agent operation"
		return m, nil
	}

	m.running = true
	m.job = jobAgent
	m.agentCommand = command
	m.agentExitCode = 0
	m.agentState = agentRunning
	m.agentActivity = ""
	if command == "show" {
		m.agentDefinition = ""
	}
	if command != "check" {
		m.agentOutput = ""
	}
	m.notice = ""
	m.viewport.GotoBottom()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.agentEvents = m.agent.Start(ctx, args, input)
	m.syncContent()
	return m, tea.Batch(waitAgentEvent(m.agentEvents), tick())
}

func (m *Model) updateAgentProcess(event agentexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case agentexec.Stdout:
			if m.agentCommand == "show" {
				m.agentDefinition = boundedAgentText(m.agentDefinition, event.Text)
			} else {
				m.agentOutput = boundedAgentText(m.agentOutput, event.Text)
			}
		case agentexec.Stderr:
			m.agentActivity = boundedAgentActivity(m.agentActivity, event.Text)
		}
		m.syncContent()
		return m, waitAgentEvent(m.agentEvents)
	}

	m.running = false
	m.cancel = nil
	m.job = 0
	m.agentExitCode = event.ExitCode
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.agentState = agentInterrupted
		m.notice = "Agent operation interrupted"
	case event.ExitCode == 0:
		m.agentState = agentSucceeded
		m.notice = ""
	case event.ExitCode == 1:
		m.agentState = agentNegative
		m.notice = m.agentNegativeNotice()
	default:
		m.agentState = agentBroken
		m.notice = filterFailure("agent "+m.agentCommand, event.ExitCode, event.Err, m.agentActivity)
	}
	m.syncContent()
	return m, nil
}

func (m *Model) agentNegativeNotice() string {
	switch m.agentCommand {
	case "show", "check":
		return "Agent home is invalid · inspect the evidence and repair its ordinary files"
	case "proposals":
		return "One or more proposals cannot be reviewed safely · inspect Agent's evidence"
	case "specialist":
		return "The specialist did not reach its own executable definition of done"
	case "run":
		return "Not accepted · the home check still rejects the standing outcome"
	case "history", "history-check":
		return "History reported no match, damage, or failed replay · inspect the JSONL result"
	default:
		return "Agent command returned a negative verdict"
	}
}

func (m *Model) updateAgentHome(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
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
	if m.agentChildOpen {
		return m.updateAgentSpecialistForm(msg, key)
	}
	switch key {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "r":
		return m.startAgentCommand("show")
	case "c":
		return m.startAgentCommand("check")
	case "g":
		return m.startAgentCommand("run")
	case "t":
		return m.startAgentCommand("tick")
	case "l":
		return m.startAgentCommand("history")
	case "v":
		return m.startAgentCommand("history-check")
	case "p":
		return m.startAgentCommand("proposals")
	case "s":
		return m.openAgentSpecialistForm()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) openAgentSpecialistForm() (tea.Model, tea.Cmd) {
	m.agentChildOpen = true
	m.agentChildFocus = 0
	m.agentChild.SetValue(m.agentChildName)
	m.composer.SetValue("")
	m.composer.Placeholder = "Give this child one self-contained, bounded task…"
	m.composer.Blur()
	m.notice = "Choose one direct child home and give it only the task it needs"
	m.viewport.GotoBottom()
	m.syncContent()
	return m, m.agentChild.Focus()
}

func (m *Model) closeAgentSpecialistForm() (tea.Model, tea.Cmd) {
	m.agentChildOpen = false
	m.agentChild.Blur()
	m.composer.Blur()
	m.composer.SetValue("")
	m.composer.Placeholder = "Describe the outcome you want, or type /help…"
	m.notice = "Specialist run cancelled; no child was invoked"
	m.syncContent()
	return m, nil
}

func (m *Model) updateAgentSpecialistForm(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeAgentSpecialistForm()
	case "tab", "shift+tab":
		if m.agentChildFocus == 0 {
			m.agentChildFocus = 1
			m.agentChild.Blur()
			m.syncContent()
			return m, m.composer.Focus()
		}
		m.agentChildFocus = 0
		m.composer.Blur()
		m.syncContent()
		return m, m.agentChild.Focus()
	case "ctrl+s", "ctrl+enter":
		return m.startAgentSpecialist()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	if m.agentChildFocus == 0 {
		m.agentChild, cmd = m.agentChild.Update(msg)
	} else {
		m.composer, cmd = m.composer.Update(msg)
	}
	m.syncContent()
	return m, cmd
}

func (m *Model) startAgentSpecialist() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.agentChild.Value())
	if name == "" {
		m.notice = "Choose a direct specialist name"
		return m, nil
	}
	task := m.composer.Value()
	if strings.TrimSpace(task) == "" {
		m.notice = "Give the specialist one bounded task"
		return m, nil
	}
	if len([]byte(task)) > agentTaskLimit {
		m.notice = fmt.Sprintf("Specialist task is %d bytes; the interactive limit is %d", len([]byte(task)), agentTaskLimit)
		return m, nil
	}
	m.agentChildName = name
	m.agentChildTask = task
	m.agentChildOpen = false
	m.agentChild.Blur()
	m.composer.Blur()
	m.composer.SetValue("")
	m.composer.Placeholder = "Describe the outcome you want, or type /help…"
	return m.startAgentCommand("specialist")
}

func waitAgentEvent(events <-chan agentexec.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return agentProcessEvent{Done: true, ExitCode: 2, Err: errors.New("agent event stream closed")}
		}
		return agentProcessEvent(event)
	}
}

func boundedAgentText(current, addition string) string {
	text := current + safeText(addition)
	runes := []rune(text)
	if len(runes) <= agentOutputLimit {
		return text
	}
	marker := []rune("[earlier output omitted from this view; .agent/runs is authoritative]\n")
	keep := agentOutputLimit - len(marker)
	return string(marker) + string(runes[len(runes)-keep:])
}

func boundedAgentActivity(current, addition string) string {
	text := current + safeText(addition)
	runes := []rune(text)
	if len(runes) <= activityLimit {
		return text
	}
	marker := []rune("[earlier stderr omitted]\n")
	keep := activityLimit - len(marker)
	return string(marker) + string(runes[len(runes)-keep:])
}

func (m *Model) renderAgentHome(width int) string {
	t := makeTheme(m.dark)
	verdict, style := m.agentVerdict(t)
	rows := []string{
		t.hero.Render(filepath.Base(m.agentHome)) + "  " + style.Render(verdict),
		t.faint.Render(m.agentCommandDisplay()),
	}
	if body := strings.TrimSpace(m.agentDefinition); body != "" {
		rows = append(rows, "", t.sessionLabel.Render("COMPILED HOME"), t.document.Width(max(16, width-5)).Render(body))
	}
	if output := strings.TrimSpace(m.agentOutput); output != "" {
		label := "OUTPUT"
		if strings.HasPrefix(m.agentCommand, "history") {
			label = "HISTORY · JSONL"
		} else if m.agentCommand == "proposals" {
			label = "PROPOSALS · READ ONLY"
		} else if m.agentCommand == "specialist" {
			label = "SPECIALIST RESULT"
		}
		rows = append(rows, "", t.sessionLabel.Render(label), t.document.Width(max(16, width-5)).Render(output))
	}
	if activity := strings.TrimSpace(m.agentActivity); activity != "" {
		rows = append(rows, "", t.sessionLabel.Render("EVIDENCE"), t.document.Width(max(16, width-5)).Render(activity))
	} else if m.running {
		rows = append(rows, "", t.working.Render(spinnerFrame(m.spinner)+"  waiting for agent"))
	}
	if m.agentChildOpen {
		label := t.faint.Render("DIRECT SPECIALIST HOME")
		if m.agentChildFocus == 0 {
			label = t.sessionLabel.Render("DIRECT SPECIALIST HOME")
		}
		rows = append(rows, "",
			t.hero.Render("Run one child with separate context, work, check, and evidence."),
			t.muted.Render("Bench passes only this name, task, and caller-selected model to agent specialist."),
			"", label, t.input.Width(max(16, width-5)).Render(m.agentChild.View()))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) agentCommandDisplay() string {
	home := strconv.Quote(m.agentHome)
	switch m.agentCommand {
	case "run", "tick":
		model := ""
		if strings.TrimSpace(m.modelName) != "" {
			model = " -m " + strconv.Quote(m.modelName)
		}
		return "agent " + m.agentCommand + model + " " + home
	case "history-check":
		return "agent history " + home + " check"
	case "history":
		return "agent history " + home
	case "specialist":
		model := ""
		if strings.TrimSpace(m.modelName) != "" {
			model = " -m " + strconv.Quote(m.modelName)
		}
		return "<" + fmt.Sprint(len([]byte(m.agentChildTask))) + "-byte task> | agent specialist " + home + " " + strconv.Quote(m.agentChildName) + model
	case "":
		return "agent show " + home
	default:
		return "agent " + m.agentCommand + " " + home
	}
}

func (m *Model) agentVerdict(t theme) (string, lipgloss.Style) {
	if m.agentState == agentRunning {
		return "… " + strings.ToUpper(strings.ReplaceAll(m.agentCommand, "-", " ")), t.sessionLabel
	}
	if m.agentState == agentInterrupted {
		return "○ INTERRUPTED", t.warning
	}
	if m.agentState == agentBroken {
		return fmt.Sprintf("! BROKEN · EXIT %d", m.agentExitCode), t.danger
	}
	if m.agentState == agentNegative {
		return fmt.Sprintf("○ NOT ACCEPTED · EXIT %d", m.agentExitCode), t.warning
	}
	if m.agentState == agentSucceeded {
		switch m.agentCommand {
		case "show", "check":
			return "✓ VALID", t.success
		case "run":
			return "✓ CHECK ACCEPTED", t.success
		case "tick":
			return "✓ TICK COMPLETE", t.success
		case "history-check":
			return "✓ REPLAY VERIFIED", t.success
		case "history":
			return "✓ HISTORY READ", t.success
		case "proposals":
			return "✓ PROPOSALS REVIEWED", t.success
		case "specialist":
			return "✓ SPECIALIST CHECK ACCEPTED", t.success
		}
		return "✓ SUCCEEDED", t.success
	}
	return "○ NOT INSPECTED", t.muted
}
