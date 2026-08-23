package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/autonomy"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type shellReturnedMsg struct{ err error }
type editorReturnedMsg struct{ err error }
type approvalReturnedMsg struct {
	digest string
	err    error
}

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
		if m.pendingApproval != nil {
			m.notice = "Approval pending · decide with May or /continue to release it before entering Ask mode"
			m.syncContent()
			return m, nil
		}
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
	case "approval", "approve":
		return m.commandApproval(args)
	case "cage":
		return m.commandCage(args)
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
		m.notice = "usage: /mode [auto|quick|review|loop]"
		return m, nil
	}
	mode, err := autonomy.Parse(args[0])
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	m.autoRequested = mode == autonomy.Auto
	concrete := mode
	if concrete == autonomy.Auto {
		concrete = autonomy.Review
	}
	m.taskOptions.IntentContract = concrete.UsesContract()
	m.taskOptions.Loop = concrete == autonomy.Loop
	m.retryContract = false
	if mode == autonomy.Quick {
		m.taskOptions.ApprovalPolicy = plyexec.ApprovalOff
		m.taskOptions.ActionConfinement = plyexec.ConfinementOff
		m.taskOptions.CheckAllCriteria = false
		m.pendingContract = nil
		m.pendingApproval = nil
		m.retryContract = false
		m.pendingDecision = nil
		m.taskOptions.Force = false
		m.continueContract = false
		m.screen = screenAsk
		m.composer.Placeholder = "Describe the outcome you want, or type /help…"
	}
	if mode == autonomy.Review || mode == autonomy.Auto {
		m.taskOptions.Loop = false
	}
	m.composer.SetValue("")
	m.notice = fmt.Sprintf("Autonomy %s · %s", mode, mode.Description())
	if mode == autonomy.Loop && strings.TrimSpace(m.taskOptions.Check) == "" {
		m.notice += " · configure /check -- COMMAND before submitting"
	}
	m.syncContent()
	return m, nil
}

func (m *Model) commandCage(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		bound, staged, isBound := m.confinementPolicyState()
		m.notice = "Action confinement · " + staged
		if isBound {
			m.notice = "Action confinement · bound " + bound
			if staged != bound {
				m.notice += " · requested for next amendment " + staged
			}
		}
		m.syncContent()
		return m, nil
	}
	if len(args) != 1 {
		m.notice = "usage: /cage [on|off]"
		return m, nil
	}
	switch strings.ToLower(args[0]) {
	case "on":
		if !m.taskOptions.IntentContract {
			m.notice = "Cage needs Review or Loop · select /mode review first"
			return m, nil
		}
		if err := cageStateOutsideWorkspace(m.workspace, m.dataDir, m.session); err != nil {
			m.notice = err.Error()
			m.syncContent()
			return m, nil
		}
		m.taskOptions.ActionConfinement = plyexec.ConfinementCage
		m.taskOptions.ApprovalPolicy = plyexec.ApprovalEveryAction
	case "off":
		m.taskOptions.ActionConfinement = plyexec.ConfinementOff
	default:
		m.notice = "usage: /cage [on|off]"
		return m, nil
	}
	m.notice = "Action confinement set to " + m.taskOptions.ActionConfinement + " for the next contract revision"
	if m.contractDraft != nil || m.admittedContract != nil {
		m.notice += " · revise or amend before it becomes admitted policy"
	}
	m.syncContent()
	return m, nil
}

func cageStateOutsideWorkspace(workspace, dataDir string, controllerPaths ...string) error {
	if configured := strings.TrimSpace(os.Getenv("BENCH_DIR")); configured != "" && !filepath.IsAbs(configured) {
		return errors.New("Cage needs an absolute external BENCH_DIR")
	}
	if !filepath.IsAbs(dataDir) {
		return errors.New("Cage needs an absolute external BENCH_DIR")
	}
	rawWork, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	work := rawWork
	if resolved, e := filepath.EvalSymlinks(rawWork); e == nil {
		work = resolved
	}
	rawState, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	state, err := cageProspectivePath(dataDir)
	if err != nil {
		return fmt.Errorf("resolve BENCH_DIR for Cage: %w", err)
	}
	if cagePathContains(rawWork, rawState) || cagePathContains(rawState, rawWork) || cagePathContains(work, rawState) || cagePathContains(rawState, work) || cagePathContains(work, state) || cagePathContains(state, work) {
		return errors.New("Cage needs BENCH_DIR outside and separate from the writable workspace")
	}
	for _, path := range controllerPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		rawTarget, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		target, err := cageProspectivePath(path)
		if err != nil {
			return fmt.Errorf("resolve Ask session for Cage: %w", err)
		}
		if cagePathContains(rawWork, rawTarget) || cagePathContains(work, rawTarget) || cagePathContains(work, target) {
			return errors.New("Cage Ask session must be outside the writable workspace")
		}
	}
	return nil
}

func cageProspectivePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur, tail := abs, []string{}
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs), nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func cagePathContains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *Model) commandApproval(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		bound, staged, isBound := m.approvalPolicyState()
		m.composer.SetValue("")
		m.notice = "Action approval · " + staged
		if isBound {
			m.notice = "Action approval · bound " + bound
			if staged != bound {
				m.notice += " · requested for next amendment " + staged
			}
		}
		m.syncContent()
		return m, nil
	}
	if len(args) != 1 {
		m.notice = "usage: /approval [off|every-action|decide]"
		return m, nil
	}
	switch strings.ToLower(args[0]) {
	case "decide":
		m.composer.SetValue("")
		return m.decidePendingApproval()
	case plyexec.ApprovalOff, plyexec.ApprovalEveryAction:
		policy := strings.ToLower(args[0])
		if policy == plyexec.ApprovalEveryAction && !m.taskOptions.IntentContract {
			m.notice = "Every-action approval needs Review or Loop · select /mode review first"
			return m, nil
		}
		m.taskOptions.ApprovalPolicy = policy
		if policy == plyexec.ApprovalOff {
			m.taskOptions.ActionConfinement = plyexec.ConfinementOff
		}
		m.composer.SetValue("")
		m.notice = "Action approval set to " + policy + " for the next contract revision"
		if m.contractDraft != nil || m.admittedContract != nil {
			m.notice += " · revise or amend before it becomes admitted policy"
		}
		m.syncContent()
		return m, nil
	default:
		m.notice = "usage: /approval [off|every-action|decide]"
		return m, nil
	}
}

func (m *Model) decidePendingApproval() (tea.Model, tea.Cmd) {
	if m.pendingApproval == nil || m.pendingApproval.ApprovalReceipt == nil {
		m.notice = "No exact May action is awaiting a decision"
		m.syncContent()
		return m, nil
	}
	digest := m.pendingApproval.ApprovalReceipt.Digest
	may := strings.TrimSpace(m.pendingApproval.ApprovalReceipt.MayPath)
	if may == "" {
		may = strings.TrimSpace(m.mayPath)
	}
	if may == "" {
		may = "may"
	}
	if expected := strings.TrimSpace(m.pendingApproval.ApprovalReceipt.MaySHA256); expected != "" {
		got, err := plyexec.ExecutableSHA256(may)
		if err != nil || got != expected {
			m.notice = "May executable changed since the action was parked · approval remains pending"
			m.syncContent()
			return m, nil
		}
	}
	m.composer.SetValue("")
	m.composer.Blur()
	m.notice = "Opening May for exact action " + digest + " · Bench cannot approve it"
	m.syncContent()
	cmd := exec.Command(may, "decide", digest)
	cmd.Dir = m.workspace
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return approvalReturnedMsg{digest: digest, err: err} })
}

func (m *Model) updateApprovalDecision(msg approvalReturnedMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.pendingApproval = nil
		m.taskOptions.Force = true
		m.messages = append(m.messages, message{role: roleOutcome, text: "APPROVED IN MAY\nExact action " + msg.digest + " has a single-use grant. Bench will rerun the same admitted outcome; only byte-identical action input can spend it."})
		m.notice = "May granted the exact action · restarting the same admission"
		return m.runAdmittedContract("The operator granted May action " + msg.digest + ". Re-propose those exact bytes only if the action is still required.")
	}
	var exit *exec.ExitError
	if errors.As(msg.err, &exit) && exit.ExitCode() == 3 {
		m.messages = append(m.messages, message{role: roleOutcome, text: "NOT GRANTED BY MAY\nExact action " + msg.digest + " was not executed. May may have recorded a decline, or the decision terminal was unavailable."})
		m.notice = "May did not grant the action · approval state is retained · retry May or /continue with another approach"
		m.syncContent()
		return m, m.composer.Focus()
	}
	m.notice = "May decision did not complete · approval remains pending · " + msg.err.Error()
	m.syncContent()
	return m, m.composer.Focus()
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
		m.notice = "usage: /contract [accept|edit|import|run|audit|amend|cancel] · /mode auto|quick|review|loop selects autonomy"
		m.syncContent()
		return m, nil
	}
	switch strings.ToLower(args[0]) {
	case "on":
		m.autoRequested = false
		m.taskOptions.IntentContract = true
		m.taskOptions.Loop = false
		m.notice = "Autonomy review · the next intent becomes an editable draft; Ply waits for /contract accept"
	case "off":
		m.autoRequested = false
		m.taskOptions.IntentContract = false
		m.taskOptions.Loop = false
		m.taskOptions.ApprovalPolicy = plyexec.ApprovalOff
		m.taskOptions.ActionConfinement = plyexec.ConfinementOff
		m.taskOptions.CheckAllCriteria = false
		m.pendingContract = nil
		m.pendingApproval = nil
		m.retryContract = false
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
		m.notice = "usage: /contract [accept|edit|import|run|audit|amend|cancel] · /mode auto|quick|review|loop selects autonomy"
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
		m.retryContract = false
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
			m.retryContract = false
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
		m.retryContract = false
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
		if m.pendingApproval != nil {
			m.notice = "Approval pending · decide with May or /continue to release it before disabling work mode"
			m.syncContent()
			return m, nil
		}
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
	boundApproval, stagedApproval, approvalBound := m.approvalPolicyState()
	approval := approvalPolicyLabel(boundApproval)
	if approvalBound {
		approval = "Bound: " + approval
		if stagedApproval != boundApproval {
			approval += " · requested for next amendment: " + approvalPolicyLabel(stagedApproval)
		}
	}
	lines := []string{
		"Mode: " + m.modeDisplay(),
		"Autonomy: " + string(m.autonomyMode()) + " · " + m.autonomyMode().Description(),
	}
	if m.autoRequested && m.lastAuto != nil {
		reason := m.lastAuto.Reason
		if m.lastAuto.Clamped != "" {
			reason = m.lastAuto.Clamped
		}
		lines = append(lines, "Last Auto route: "+string(m.lastAuto.Effective)+" · "+reason)
	}
	lines = append(lines,
		"Model: "+m.modelDisplay(),
		"Tools: "+tools,
		"Outcome contract: "+contract,
		"Check: "+check,
		"Action approval: "+approval,
		"Action confinement: "+m.confinementStatus(),
		"Brief skills: "+skills,
		"Session evidence: "+m.session,
		"Contract files: "+m.contractStore.DraftPath(),
		"Subagent evidence: "+m.subagentsPath(),
	)
	return strings.Join(lines, "\n")
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
	} else if m.pendingApproval != nil {
		parts = append(parts, "May approval pending")
	} else if m.taskOptions.Force {
		parts = append(parts, "continue armed")
	}
	boundApproval, stagedApproval, approvalBound := m.approvalPolicyState()
	if boundApproval == plyexec.ApprovalEveryAction {
		parts = append(parts, "May before every action")
	}
	if approvalBound && stagedApproval != boundApproval {
		parts = append(parts, "next amendment approval="+stagedApproval)
	}
	boundConfinement, stagedConfinement, confinementBound := m.confinementPolicyState()
	if boundConfinement == plyexec.ConfinementCage {
		parts = append(parts, "Cage around every approved action")
	}
	if confinementBound && stagedConfinement != boundConfinement {
		parts = append(parts, "next amendment confinement="+stagedConfinement)
	}
	if m.taskOptions.HasCycles {
		parts = append(parts, "cycles="+plyexec.LoopCycleBudget(m.taskOptions))
	} else if m.taskOptions.Loop {
		parts = append(parts, "cycles=unbounded")
	}
	if m.taskOptions.HasTurns {
		if m.taskOptions.Loop {
			parts = append(parts, "turns="+plyexec.LoopTurnBudget(m.taskOptions))
		} else {
			parts = append(parts, fmt.Sprintf("turns=%d", m.taskOptions.Turns))
		}
	} else if m.taskOptions.Loop {
		parts = append(parts, fmt.Sprintf("turns=%d", plyexec.DefaultLoopTurns))
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

func (m *Model) confinementPolicyState() (bound, staged string, isBound bool) {
	staged = strings.TrimSpace(m.taskOptions.ActionConfinement)
	if staged == "" {
		staged = plyexec.ConfinementOff
	}
	if m.contractDraft != nil {
		bound, isBound = m.contractDraft.ActionConfinement, true
	} else if m.admittedContract != nil {
		bound, isBound = m.admittedContract.ActionConfinement, true
	}
	if strings.TrimSpace(bound) == "" {
		bound = plyexec.ConfinementOff
	}
	if !isBound {
		bound = staged
	}
	return
}

func (m *Model) confinementStatus() string {
	bound, staged, isBound := m.confinementPolicyState()
	label := func(v string) string {
		if v == plyexec.ConfinementCage {
			return "Cage · workspace + private temp writable · network denied · host reads unrestricted"
		}
		return "Off"
	}
	result := label(bound)
	if isBound {
		result = "Bound: " + result
		if staged != bound {
			result += " · requested for next amendment: " + label(staged)
		}
	}
	return result
}

func (m *Model) approvalPolicyState() (bound, staged string, isBound bool) {
	staged = strings.TrimSpace(m.taskOptions.ApprovalPolicy)
	if staged == "" {
		staged = plyexec.ApprovalOff
	}
	if m.contractDraft != nil {
		bound = strings.TrimSpace(m.contractDraft.ApprovalPolicy)
		isBound = true
	} else if m.admittedContract != nil {
		bound = strings.TrimSpace(m.admittedContract.ApprovalPolicy)
		isBound = true
	}
	if bound == "" {
		bound = plyexec.ApprovalOff
	}
	if !isBound {
		bound = staged
	}
	return bound, staged, isBound
}

func approvalPolicyLabel(policy string) string {
	if policy == plyexec.ApprovalEveryAction {
		return "Every action · exact May decision immediately before execution"
	}
	return "Off"
}
