package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
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
		m.notice = m.statusLine()
		m.syncContent()
		return m, nil
	case "model":
		return m.commandModel(args)
	case "tools":
		return m.commandTools(args)
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

func (m *Model) commandContract(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.composer.SetValue("")
		if m.taskOptions.IntentContract {
			m.notice = "Outcome contracts on · Bench compiles intent before Ply works"
		} else {
			m.notice = "Outcome contracts off · intent goes directly to Ply"
		}
		m.syncContent()
		return m, nil
	}
	if len(args) != 1 {
		m.notice = "usage: /contract on|off"
		m.syncContent()
		return m, nil
	}
	switch strings.ToLower(args[0]) {
	case "on":
		m.taskOptions.IntentContract = true
		m.notice = "Outcome contracts on · the next intent will be compiled and logged before work"
	case "off":
		m.taskOptions.IntentContract = false
		m.notice = "Outcome contracts off · the next intent will go directly to Ply"
	default:
		m.notice = "usage: /contract on|off"
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
		}
		m.syncContent()
		return m, nil
	}
	if strings.EqualFold(rest, "off") {
		m.taskOptions.Check = ""
		m.composer.SetValue("")
		m.notice = "Check cleared · the next work outcome will be unchecked"
		m.syncContent()
		return m, nil
	}
	if rest == "--" {
		m.notice = "usage: /check -- COMMAND · /check off"
		m.syncContent()
		return m, nil
	}
	if strings.HasPrefix(rest, "--") && len(rest) > 2 && (rest[2] == ' ' || rest[2] == '\t') {
		check := strings.TrimLeft(rest[2:], " \t")
		if strings.TrimSpace(check) == "" {
			m.notice = "usage: /check -- COMMAND · /check off"
			m.syncContent()
			return m, nil
		}
		m.taskOptions.Check = check
		m.composer.SetValue("")
		m.notice = "Check set for the next outcome · " + strconv.Quote(check)
		m.syncContent()
		return m, nil
	}
	m.notice = "usage: /check -- COMMAND · /check off"
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

func (m *Model) taskPolicyDisplay() string {
	contract := "intent contract off"
	if m.taskOptions.IntentContract {
		contract = "intent contract on"
	}
	parts := []string{"work runs unchecked", contract}
	if m.taskOptions.Check != "" {
		parts[0] = "work check " + strconv.Quote(m.taskOptions.Check)
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
