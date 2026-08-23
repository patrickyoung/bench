package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/autonomy"
)

type shellReturnedMsg struct{ err error }
type editorReturnedMsg struct{ err error }

// handleCommand interprets an explicit slash command. A leading // is handled
// by submit before this point and sends one literal slash to the model.
func (m *Model) handleCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return m, nil
	}
	name := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	args := fields[1:]
	switch name {
	case "help", "?":
		m.composer.SetValue("")
		m.showHelp = true
		m.composer.Blur()
		m.syncContent()
		m.viewport.GotoTop()
		return m, nil
	case "quit", "exit":
		return m, tea.Quit
	case "status":
		m.composer.SetValue("")
		m.messages = append(m.messages, message{role: roleStatus, text: safeText(m.statusReport())})
		m.notice = "Status shown in the transcript · " + m.statusLine()
		m.syncContent()
		return m, nil
	case "model":
		return m.commandModel(args)
	case "tools":
		return m.commandTools(args)
	case "mode":
		return m.commandAutonomy(args)
	case "ask":
		m.composer.SetValue("")
		m.taskMode = false
		m.notice = "Ask mode · model answers without running commands"
		m.syncContent()
		return m, nil
	case "work", "ply":
		m.composer.SetValue("")
		m.taskMode = true
		m.notice = "Ask + Ply mode · " + m.toolGrant()
		m.syncContent()
		return m, nil
	case "check":
		return m.commandCheck(line)
	case "contract":
		return m.commandContract(args)
	case "accept":
		return m.commandAccept(args)
	case "continue":
		return m.commandContinue(args)
	case "skills":
		m.composer.SetValue("")
		return m.openSkills()
	case "agent", "design":
		m.composer.SetValue(strings.Join(args, " "))
		return m.openDesign()
	case "shell", "sh":
		if len(args) != 0 {
			m.notice = "/shell takes no command; it opens $SHELL interactively"
			m.syncContent()
			return m, nil
		}
		m.composer.SetValue("")
		m.composer.Blur()
		m.notice = "Opening operator shell · exit returns to Bench"
		m.syncContent()
		return m, m.openShell()
	default:
		m.notice = fmt.Sprintf("Unknown command /%s · /help lists commands; //%s sends it literally", name, name)
		m.syncContent()
		return m, nil
	}
}

func (m *Model) commandAutonomy(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.composer.SetValue("")
		mode := m.autonomyMode()
		m.notice = fmt.Sprintf("Autonomy %s · %s", mode, mode.Description())
		m.syncContent()
		return m, nil
	}
	if len(args) != 1 {
		m.notice = "usage: /mode [quick|review]"
		return m, nil
	}
	mode, err := autonomy.Parse(args[0])
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	m.taskOptions.IntentContract = mode.UsesContract()
	if mode == autonomy.Quick {
		m.taskOptions.CheckAllCriteria = false
		m.pendingContract = nil
		m.pendingDecision = nil
		m.taskOptions.Force = false
		m.continueContract = false
		m.screen = screenAsk
		m.composer.Placeholder = "Describe the outcome you want, or type /help…"
	}
	m.composer.SetValue("")
	m.notice = fmt.Sprintf("Autonomy %s · %s", mode, mode.Description())
	m.syncContent()
	return m, nil
}

func (m *Model) commandContract(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.composer.SetValue("")
		if !m.taskOptions.IntentContract {
			m.notice = "Autonomy quick · use /mode review to negotiate an outcome contract"
			m.syncContent()
			return m, nil
		}
		return m.openContract()
	}
	if len(args) != 1 {
		m.notice = "usage: /contract [accept|edit|import|run|audit|amend|cancel] · /mode quick|review selects autonomy"
		m.syncContent()
		return m, nil
	}
	switch strings.ToLower(args[0]) {
	case "on":
		m.taskOptions.IntentContract = true
		m.notice = "Autonomy review · the next intent becomes an editable draft; Ply waits for /contract accept"
	case "off":
		m.taskOptions.IntentContract = false
		m.taskOptions.CheckAllCriteria = false
		m.pendingContract = nil
		m.pendingDecision = nil
		m.taskOptions.Force = false
		m.continueContract = false
		m.screen = screenAsk
		m.composer.Placeholder = "Describe the outcome you want, or type /help…"
		m.notice = "Autonomy quick · the next outcome skips contract review and starts Ply with the current tool grant"
	case "accept", "admit":
		m.composer.SetValue("")
		return m.acceptContractDraft()
	case "edit":
		m.composer.SetValue("")
		return m.editContract()
	case "import":
		m.composer.SetValue("")
		if m.contractDraft == nil {
			m.notice = "No editable contract draft is available to import"
			break
		}
		return m.reloadContractDraft()
	case "run":
		m.composer.SetValue("")
		return m.runAdmittedContract("")
	case "audit":
		m.composer.SetValue("")
		updated, cmd := m.openContract()
		opened := updated.(*Model)
		if opened.screen != screenContract {
			return opened, cmd
		}
		opened.contractAudit = true
		opened.notice = "Contract audit details shown · press a to return to semantic review"
		opened.syncContent()
		return opened, cmd
	case "amend":
		m.composer.SetValue("")
		return m.openContract()
	case "cancel":
		m.composer.SetValue("")
		m.screen = screenAsk
		m.composer.Placeholder = "Describe the outcome you want, or type /help…"
		m.notice = "Contract retained · /contract reopens it"
	default:
		m.notice = "usage: /contract [accept|edit|import|run|audit|amend|cancel] · /mode quick|review selects autonomy"
		m.syncContent()
		return m, nil
	}
	m.composer.SetValue("")
	m.syncContent()
	return m, nil
}

func (m *Model) commandCheck(line string) (tea.Model, tea.Cmd) {
	// Parse the delimiter, not the command. Everything after `--` is a
	// literal shell-backed verifier and must reach Ply without fields,
	// quoting, or whitespace inside it being reinterpreted by Bench.
	rest := strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(line), strings.Fields(line)[0]), " \t")
	if rest == "" {
		m.composer.SetValue("")
		if m.taskOptions.Check == "" {
			m.notice = "No check for the next outcome · run unchecked, or set one with /check -- COMMAND"
		} else {
			m.notice = "Check for the next outcome · " + strconv.Quote(m.taskOptions.Check)
			if m.taskOptions.CheckAllCriteria {
				m.notice += " · operator admits it as judge of all contract criteria"
			}
		}
		m.syncContent()
		return m, nil
	}
	if strings.EqualFold(rest, "off") {
		m.taskOptions.Check = ""
		m.taskOptions.CheckAllCriteria = false
		m.pendingContract = nil
		m.taskOptions.Force = false
		m.composer.SetValue("")
		m.notice = "Check cleared · the next work outcome will be unchecked"
		m.syncContent()
		return m, nil
	}
	if strings.EqualFold(rest, "all") {
		if m.taskOptions.Check == "" {
			m.notice = "Set a check first with /check -- COMMAND"
		} else if !m.taskOptions.IntentContract {
			m.notice = "/check all needs outcome contracts on"
		} else {
			m.taskOptions.CheckAllCriteria = true
			m.notice = "Check judges all criteria for the next outcome · operator blanket admission is armed"
		}
		m.composer.SetValue("")
		m.syncContent()
		return m, nil
	}
	if rest == "--" {
		m.notice = "usage: /check -- COMMAND · /check all · /check off"
		m.syncContent()
		return m, nil
	}
	if strings.HasPrefix(rest, "--") && len(rest) > 2 && (rest[2] == ' ' || rest[2] == '\t') {
		check := strings.TrimLeft(rest[2:], " \t")
		if strings.TrimSpace(check) == "" {
			m.notice = "usage: /check -- COMMAND · /check all · /check off"
			m.syncContent()
			return m, nil
		}
		m.taskOptions.Check = check
		m.taskOptions.CheckAllCriteria = false
		m.pendingContract = nil
		m.taskOptions.Force = false
		m.composer.SetValue("")
		m.notice = "Check set for the next outcome · " + strconv.Quote(check)
		m.syncContent()
		return m, nil
	}
	m.notice = "usage: /check -- COMMAND · /check all · /check off"
	m.syncContent()
	return m, nil
}

func (m *Model) commandModel(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.composer.SetValue("")
		m.notice = "Model · " + m.modelDisplay() + " · set with /model provider/model"
		m.syncContent()
		return m, nil
	}
	if len(args) != 1 {
		m.notice = "usage: /model provider/model · /model default"
		m.syncContent()
		return m, nil
	}
	spec := strings.TrimSpace(args[0])
	if spec == "default" {
		m.modelName = m.modelDefault
	} else {
		provider, model, ok := strings.Cut(spec, "/")
		if !ok || provider == "" || model == "" {
			m.notice = "Model must be provider/model, for example openai-codex/gpt-5.3-codex"
			m.syncContent()
			return m, nil
		}
		m.modelName = spec
	}
	m.composer.SetValue("")
	m.notice = "Model switched for future work · " + m.modelDisplay()
	m.syncContent()
	return m, nil
}

func (m *Model) commandTools(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.composer.SetValue("")
		m.notice = "Mode · " + m.modeDisplay() + " · /tools shell|off|PATH"
		m.syncContent()
		return m, nil
	}
	if len(args) != 1 {
		m.notice = "usage: /tools shell|off|PATH"
		m.syncContent()
		return m, nil
	}
	choice := args[0]
	switch strings.ToLower(choice) {
	case "off", "none", "ask":
		m.taskMode = false
	case "on", "ply", "work":
		m.taskMode = true
	case "shell", "full":
		m.taskMode = true
		m.toolbox = ""
	default:
		path := choice
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.workspace, path)
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			m.notice = "Toolbox is not a directory · " + path
			m.syncContent()
			return m, nil
		}
		m.taskMode = true
		m.toolbox = filepath.Clean(path)
	}
	m.composer.SetValue("")
	m.notice = "Mode switched · " + m.modeDisplay()
	m.syncContent()
	return m, nil
}

func (m *Model) openShell() tea.Cmd {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = m.workspace
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return shellReturnedMsg{err: err} })
}

func (m *Model) openEditor(path string) tea.Cmd {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Dir = m.workspace
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorReturnedMsg{err: err} })
}

func (m *Model) modelDisplay() string {
	if model := strings.TrimSpace(m.modelName); model != "" {
		return model
	}
	return "Ask default"
}

func (m *Model) toolGrant() string {
	if m.toolbox != "" {
		return "toolbox " + filepath.Base(m.toolbox)
	}
	return "full shell"
}

func (m *Model) modeDisplay() string {
	if !m.taskMode {
		return "Ask · no model-run tools"
	}
	return "Ask + Ply · " + m.toolGrant()
}

func (m *Model) statusLine() string {
	skills := "no skills"
	if len(m.activeSkills) == 1 {
		skills = "1 skill"
	} else if len(m.activeSkills) > 1 {
		skills = fmt.Sprintf("%d skills", len(m.activeSkills))
	}
	return m.modeDisplay() + " · " + m.modelDisplay() + " · " + skills + " · " + m.taskPolicyDisplay() + " · subagents " + m.subagentsPath()
}

func (m *Model) statusReport() string {
	tools := "No model-run tools"
	if m.taskMode {
		tools = m.toolGrant()
	}
	contract := "Off · intent goes directly to Ply"
	if m.taskOptions.IntentContract {
		contract = "On · durable editable draft; explicit admission before workspace work"
	}
	check := "None · completion will require review"
	if m.taskOptions.Check != "" {
		check = strconv.Quote(m.taskOptions.Check) + " · verifier only"
		if m.taskOptions.CheckAllCriteria {
			check = strconv.Quote(m.taskOptions.Check) + " · operator-admitted for every criterion"
		}
	}
	skills := "None"
	if len(m.activeSkills) > 0 {
		skills = strings.Join(m.activeSkills, " · ") + " · shape contract and work, never the verdict"
	}
	return strings.Join([]string{
		"Mode: " + m.modeDisplay(),
		"Autonomy: " + string(m.autonomyMode()) + " · " + m.autonomyMode().Description(),
		"Model: " + m.modelDisplay(),
		"Tools: " + tools,
		"Outcome contract: " + contract,
		"Check: " + check,
		"Brief skills: " + skills,
		"Session evidence: " + m.session,
		"Contract files: " + m.contractStore.DraftPath(),
		"Subagent evidence: " + m.subagentsPath(),
	}, "\n")
}

func (m *Model) taskPolicyDisplay() string {
	contract := "autonomy " + string(m.autonomyMode())
	parts := []string{"work runs unchecked", contract}
	if m.taskOptions.Check != "" {
		parts[0] = "work check " + strconv.Quote(m.taskOptions.Check)
		if m.taskOptions.CheckAllCriteria {
			parts[0] += " · judges all contract criteria"
		}
	}
	if m.contractDraft != nil {
		parts = append(parts, "contract draft pending admission")
	} else if m.pendingContract != nil {
		parts = append(parts, "review pending")
	} else if m.taskOptions.Force {
		parts = append(parts, "continue armed")
	}
	if m.taskOptions.HasCycles {
		parts = append(parts, fmt.Sprintf("cycles=%d", m.taskOptions.Cycles))
	}
	if m.taskOptions.HasTurns {
		parts = append(parts, fmt.Sprintf("turns=%d", m.taskOptions.Turns))
	}
	if m.taskOptions.HasTimeout {
		parts = append(parts, "timeout="+m.taskOptions.Timeout.String())
	}
	if m.taskOptions.Compact {
		parts = append(parts, "compact")
	}
	if m.taskOptions.HasCompactions {
		parts = append(parts, fmt.Sprintf("compactions=%d", m.taskOptions.Compactions))
	}
	return strings.Join(parts, " · ")
}
