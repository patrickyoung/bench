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
	case "learn-why":
		args = []string{"learn", "-into", m.agentLearnName, "-why", m.agentHome, m.agentLearnSession}
	case "learn-prepare":
		args = []string{"learn", "-into", m.agentLearnName}
		if model := strings.TrimSpace(m.modelName); model != "" {
			args = append(args, "-m", model)
		}
		args = append(args, "-prepare", m.agentLearnProposalName, m.agentHome, m.agentLearnSession)
	case "learn-show":
		args = []string{"learn", "-show", m.agentLearnProposalName, m.agentHome}
	case "learn-admit":
		args = []string{"learn", "-admit", m.agentLearnProposalName, m.agentHome}
	case "amend":
		args = []string{"amend", m.agentHome, m.agentAmendName}
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
		if m.agentCommand == "amend" || m.agentCommand == "learn-admit" {
			m.agentDefinition = ""
			if m.agentCommand == "amend" {
				m.notice = "Amendment applied and checked; press r to inspect the refreshed compiled home"
			} else {
				m.notice = "Reviewed lesson admitted; press r to inspect the refreshed compiled home"
			}
		}
	case m.agentCommand == "amend" && event.ExitCode == 75:
		m.agentState = agentApprovalPending
		m.notice = "Exact May approval parked; the definition is unchanged · decide the printed digest separately, then rerun this patch"
	case m.agentCommand == "amend" && event.ExitCode == 3:
		m.agentState = agentNegative
		m.notice = "May declined this exact amendment; the definition is unchanged"
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
	case "learn-why":
		return "This run has no replay-verified recovery to learn from"
	case "learn-prepare":
		return "This run produced no exact lesson proposal; the skill is unchanged"
	case "learn-show":
		return "The learning proposal could not be shown as exact reviewed bytes"
	case "learn-admit":
		return "Nothing was admitted; the run may already be learned or no longer trustworthy"
	case "amend":
		return "The home or proposal was rejected before amendment; the definition is unchanged"
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
	if m.agentLearnOpen {
		return m.updateAgentLearnForm(msg, key)
	}
	if m.agentAmendOpen {
		return m.updateAgentAmendForm(msg, key)
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
	case "h":
		return m.openAgentLearnForm()
	case "n":
		return m.openAgentLearnPrepareForm()
	case "o":
		return m.openAgentLearnProposalForm(agentLearnShow)
	case "u":
		return m.openAgentLearnProposalForm(agentLearnAdmit)
	case "a":
		return m.openAgentAmendForm()
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

func (m *Model) openAgentLearnForm() (tea.Model, tea.Cmd) {
	return m.openAgentLearning(agentLearnEvidence)
}

func (m *Model) openAgentLearnPrepareForm() (tea.Model, tea.Cmd) {
	return m.openAgentLearning(agentLearnPrepare)
}

func (m *Model) openAgentLearnProposalForm(mode agentLearnMode) (tea.Model, tea.Cmd) {
	return m.openAgentLearning(mode)
}

func (m *Model) openAgentLearning(mode agentLearnMode) (tea.Model, tea.Cmd) {
	m.agentLearnOpen = true
	m.agentLearnMode = mode
	m.agentLearnFocus = 0
	m.agentLearnSkill.SetValue(m.agentLearnName)
	m.agentLearnRun.SetValue(m.agentLearnSession)
	m.agentLearnProposal.SetValue(m.agentLearnProposalName)
	switch mode {
	case agentLearnPrepare:
		m.notice = "Name one destination, verified home session, and new proposal file; the skill will not change"
	case agentLearnShow:
		m.notice = "Name one exact learning proposal to reopen without a model or write"
	case agentLearnAdmit:
		m.notice = "Name one exact reviewed proposal to replay-check and admit without a model"
	default:
		m.notice = "Name a destination skill and one home session to inspect through Hone -why"
	}
	m.viewport.GotoBottom()
	m.syncContent()
	return m, m.focusAgentLearnField()
}

func (m *Model) closeAgentLearnForm() (tea.Model, tea.Cmd) {
	m.agentLearnOpen = false
	m.agentLearnSkill.Blur()
	m.agentLearnRun.Blur()
	m.agentLearnProposal.Blur()
	if m.agentLearnMode == agentLearnPrepare {
		m.notice = "Learning proposal cancelled; no model was called and no artifact or skill was changed"
	} else if m.agentLearnMode == agentLearnAdmit {
		m.notice = "Learning admission cancelled; no proposal or skill was changed"
	} else {
		m.notice = "Learning review cancelled; no model was called and no skill was changed"
	}
	m.syncContent()
	return m, nil
}

func (m *Model) agentLearnFieldCount() int {
	switch m.agentLearnMode {
	case agentLearnPrepare:
		return 3
	case agentLearnShow, agentLearnAdmit:
		return 1
	default:
		return 2
	}
}

func (m *Model) focusAgentLearnField() tea.Cmd {
	m.agentLearnSkill.Blur()
	m.agentLearnRun.Blur()
	m.agentLearnProposal.Blur()
	if m.agentLearnMode == agentLearnShow || m.agentLearnMode == agentLearnAdmit {
		return m.agentLearnProposal.Focus()
	}
	switch m.agentLearnFocus {
	case 0:
		return m.agentLearnSkill.Focus()
	case 1:
		return m.agentLearnRun.Focus()
	default:
		return m.agentLearnProposal.Focus()
	}
}

func (m *Model) updateAgentLearnForm(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeAgentLearnForm()
	case "tab", "shift+tab":
		count := m.agentLearnFieldCount()
		if count > 1 {
			delta := 1
			if key == "shift+tab" {
				delta = -1
			}
			m.agentLearnFocus = (m.agentLearnFocus + delta + count) % count
		}
		m.syncContent()
		return m, m.focusAgentLearnField()
	case "ctrl+s", "ctrl+enter":
		return m.startAgentLearning()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	if m.agentLearnMode == agentLearnShow || m.agentLearnMode == agentLearnAdmit || m.agentLearnFocus == 2 {
		m.agentLearnProposal, cmd = m.agentLearnProposal.Update(msg)
	} else if m.agentLearnFocus == 0 {
		m.agentLearnSkill, cmd = m.agentLearnSkill.Update(msg)
	} else {
		m.agentLearnRun, cmd = m.agentLearnRun.Update(msg)
	}
	m.syncContent()
	return m, cmd
}

func (m *Model) startAgentLearning() (tea.Model, tea.Cmd) {
	if m.agentLearnMode != agentLearnShow && m.agentLearnMode != agentLearnAdmit && strings.TrimSpace(m.agentLearnSkill.Value()) == "" {
		if m.agentLearnMode == agentLearnPrepare {
			m.notice = "Name the destination skill for the exact learning proposal"
		} else {
			m.notice = "Name the destination skill whose possible amendment you are reviewing"
		}
		return m, nil
	}
	if m.agentLearnMode != agentLearnShow && m.agentLearnMode != agentLearnAdmit && strings.TrimSpace(m.agentLearnRun.Value()) == "" {
		m.notice = "Name one replayable session from this agent home"
		return m, nil
	}
	if m.agentLearnMode != agentLearnEvidence && strings.TrimSpace(m.agentLearnProposal.Value()) == "" {
		m.notice = "Name one portable .json learning proposal"
		return m, nil
	}
	if m.agentLearnMode == agentLearnEvidence || m.agentLearnMode == agentLearnPrepare {
		m.agentLearnName = strings.TrimSpace(m.agentLearnSkill.Value())
		m.agentLearnSession = strings.TrimSpace(m.agentLearnRun.Value())
	}
	if m.agentLearnMode != agentLearnEvidence {
		m.agentLearnProposalName = strings.TrimSpace(m.agentLearnProposal.Value())
	}
	m.agentLearnOpen = false
	m.agentLearnSkill.Blur()
	m.agentLearnRun.Blur()
	m.agentLearnProposal.Blur()
	command := "learn-why"
	switch m.agentLearnMode {
	case agentLearnPrepare:
		command = "learn-prepare"
	case agentLearnShow:
		command = "learn-show"
	case agentLearnAdmit:
		command = "learn-admit"
	}
	return m.startAgentCommand(command)
}

func (m *Model) openAgentAmendForm() (tea.Model, tea.Cmd) {
	m.agentAmendOpen = true
	m.agentAmend.SetValue(m.agentAmendName)
	m.notice = "Name one patch already inspected with p; Agent will bind its exact bytes into May"
	m.viewport.GotoBottom()
	m.syncContent()
	return m, m.agentAmend.Focus()
}

func (m *Model) closeAgentAmendForm() (tea.Model, tea.Cmd) {
	m.agentAmendOpen = false
	m.agentAmend.Blur()
	m.notice = "Amendment cancelled; Agent and May were not invoked"
	m.syncContent()
	return m, nil
}

func (m *Model) updateAgentAmendForm(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeAgentAmendForm()
	case "ctrl+s", "ctrl+enter":
		return m.startAgentAmend()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.agentAmend, cmd = m.agentAmend.Update(msg)
	m.syncContent()
	return m, cmd
}

func (m *Model) startAgentAmend() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.agentAmend.Value())
	if name == "" {
		m.notice = "Name one reviewed .patch file from this home's work/proposals directory"
		return m, nil
	}
	m.agentAmendName = name
	m.agentAmendOpen = false
	m.agentAmend.Blur()
	return m.startAgentCommand("amend")
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
		} else if m.agentCommand == "learn-why" {
			label = "LEARNING EVIDENCE · READ ONLY"
		} else if m.agentCommand == "learn-prepare" {
			label = "EXACT LEARNING PROPOSAL · SKILL UNCHANGED"
		} else if m.agentCommand == "learn-show" {
			label = "EXACT LEARNING PROPOSAL · READ ONLY"
		} else if m.agentCommand == "learn-admit" {
			label = "REVIEWED LEARNING ADMISSION"
		} else if m.agentCommand == "amend" {
			label = "AMENDMENT APPROVAL / RESULT"
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
	if m.agentLearnOpen {
		proposalLabel := t.faint.Render("LEARNING PROPOSAL FILE")
		if m.agentLearnMode == agentLearnShow || m.agentLearnMode == agentLearnAdmit || m.agentLearnFocus == 2 {
			proposalLabel = t.sessionLabel.Render("LEARNING PROPOSAL FILE")
		}
		if m.agentLearnMode == agentLearnShow || m.agentLearnMode == agentLearnAdmit {
			hero := "Reopen one exact proposed skill without calls or writes."
			detail := "Hone show validates the artifact and prints its literal final SKILL.md bytes."
			if m.agentLearnMode == agentLearnAdmit {
				hero = "Admit one exact reviewed learning proposal."
				detail = "Hone replays both provenance sessions, rejects stale bytes or paths, and calls no model."
			}
			rows = append(rows, "", t.hero.Render(hero), t.muted.Render(detail),
				"", proposalLabel, t.input.Width(max(16, width-5)).Render(m.agentLearnProposal.View()))
		} else {
			skillLabel := t.faint.Render("DESTINATION SKILL")
			runLabel := t.faint.Render("HOME SESSION EVIDENCE")
			if m.agentLearnFocus == 0 {
				skillLabel = t.sessionLabel.Render("DESTINATION SKILL")
			} else if m.agentLearnFocus == 1 {
				runLabel = t.sessionLabel.Render("HOME SESSION EVIDENCE")
			}
			hero := "Review what one verified recovery could teach."
			detail := "This invokes Hone -why: replay verification and evidence extraction, with no model call or skill write."
			if m.agentLearnMode == agentLearnPrepare {
				hero = "Prepare exact proposed skill bytes from one verified recovery."
				detail = "The selected model words one user-named artifact; Hone changes no skill and never overwrites a proposal."
			}
			rows = append(rows, "", t.hero.Render(hero), t.muted.Render(detail),
				"", skillLabel, t.input.Width(max(16, width-5)).Render(m.agentLearnSkill.View()),
				"", runLabel, t.input.Width(max(16, width-5)).Render(m.agentLearnRun.View()))
			if m.agentLearnMode == agentLearnPrepare {
				rows = append(rows, "", proposalLabel, t.input.Width(max(16, width-5)).Render(m.agentLearnProposal.View()))
			}
		}
	}
	if m.agentAmendOpen {
		rows = append(rows, "",
			t.hero.Render("Apply one exact, separately approved definition amendment."),
			t.muted.Render("Review with p first. Agent owns patch validation, May binding, stale checks, rollback, and the receipt."),
			"", t.sessionLabel.Render("REVIEWED PATCH FILE"), t.input.Width(max(16, width-5)).Render(m.agentAmend.View()))
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
	case "learn-why":
		return "agent learn -into " + strconv.Quote(m.agentLearnName) + " -why " + home + " " + strconv.Quote(m.agentLearnSession)
	case "learn-prepare":
		model := ""
		if strings.TrimSpace(m.modelName) != "" {
			model = " -m " + strconv.Quote(m.modelName)
		}
		return "agent learn -into " + strconv.Quote(m.agentLearnName) + model + " -prepare " + strconv.Quote(m.agentLearnProposalName) + " " + home + " " + strconv.Quote(m.agentLearnSession)
	case "learn-show":
		return "agent learn -show " + strconv.Quote(m.agentLearnProposalName) + " " + home
	case "learn-admit":
		return "agent learn -admit " + strconv.Quote(m.agentLearnProposalName) + " " + home
	case "amend":
		return "agent amend " + home + " " + strconv.Quote(m.agentAmendName)
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
		if m.agentCommand == "amend" && m.agentExitCode == 3 {
			return "○ AMENDMENT DECLINED · EXIT 3", t.warning
		}
		return fmt.Sprintf("○ NOT ACCEPTED · EXIT %d", m.agentExitCode), t.warning
	}
	if m.agentState == agentApprovalPending {
		return "◇ APPROVAL PARKED · EXIT 75", t.warning
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
		case "learn-why":
			return "✓ LEARNING EVIDENCE REVIEWED", t.success
		case "learn-prepare":
			return "✓ EXACT LESSON PREPARED · SKILL UNCHANGED", t.success
		case "learn-show":
			return "✓ EXACT LESSON REVIEWED · READ ONLY", t.success
		case "learn-admit":
			return "✓ REVIEWED LESSON ADMITTED", t.success
		case "amend":
			return "✓ AMENDMENT APPLIED", t.success
		}
		return "✓ SUCCEEDED", t.success
	}
	return "○ NOT INSPECTED", t.muted
}
