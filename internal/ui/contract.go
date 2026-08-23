package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/contractexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type manualContractMsg struct {
	draft contractexec.Draft
	err   error
}

func waitContractDraftEvent(events <-chan contractexec.DraftEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return contractDraftProcessEvent{Done: true, ExitCode: 2, Err: errors.New("contract compiler event stream closed")}
		}
		return contractDraftProcessEvent(event)
	}
}

func (m *Model) updateContractDraftProcess(event contractexec.DraftEvent) (tea.Model, tea.Cmd) {
	if !event.Done {
		if event.Stream == askexec.Stderr {
			m.appendActivity(safeText(event.Text))
		}
		m.syncContent()
		return m, waitContractDraftEvent(m.contractEvents)
	}
	m.running = false
	m.cancel = nil
	m.job = 0
	m.activity = ""
	if event.Err != nil || event.ExitCode != 0 || event.Draft == nil {
		m.notice = "Contract draft not updated · " + failureDetail(event.ExitCode, event.Err)
		m.syncContent()
		return m, m.composer.Focus()
	}
	draft := *event.Draft
	previous := m.contractDraft
	if previous == nil {
		previous = m.admittedContract
	}
	m.contractDraft = &draft
	m.admittedContract = nil
	m.contractAudit = false
	m.screen = screenContract
	m.composer.SetValue("")
	m.composer.Placeholder = "Describe a contract change, or use /contract accept…"
	m.composer.Focus()
	m.messages = append(m.messages, message{role: roleContractDraft, text: contractDraftCard(draft, previous, m.contractStore.DraftPath(), m.taskOptions)})
	m.notice = fmt.Sprintf("Contract draft r%d saved · review, revise, edit, or explicitly accept", draft.Generation)
	m.syncContent()
	return m, m.composer.Focus()
}

func (m *Model) updateContractScreen(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		if key == "ctrl+c" || key == "esc" {
			m.interrupt()
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.screen = screenAsk
		m.composer.SetValue("")
		m.composer.Placeholder = "Describe the outcome you want, or type /help…"
		m.notice = "Contract retained · /contract reopens it"
		m.syncContent()
		return m, m.composer.Focus()
	case "e":
		if strings.TrimSpace(m.composer.Value()) == "" {
			return m.editContract()
		}
	case "a":
		if strings.TrimSpace(m.composer.Value()) == "" {
			m.contractAudit = !m.contractAudit
			if m.contractAudit {
				m.notice = "Contract audit details shown · press a to return to the semantic review"
			} else {
				m.notice = "Contract audit details hidden"
			}
			m.syncContent()
			return m, nil
		}
	case "ctrl+s", "ctrl+enter":
		return m.acceptContractDraft()
	case "enter":
		return m.submitContractRevision()
	case "alt+enter", "shift+enter":
		m.composer.InsertString("\n")
		m.syncContent()
		return m, nil
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

func (m *Model) submitContractRevision() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		m.notice = "Describe the change, press e to edit JSON, or /contract accept"
		m.syncContent()
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.handleCommand(text)
	}
	current := m.contractDraft
	if current == nil && m.admittedContract != nil {
		amendment := *m.admittedContract
		amendment.ParentRevisionID = amendment.RevisionID
		current = &amendment
	}
	if current == nil {
		m.notice = "No contract is available to revise"
		return m, nil
	}
	m.messages = append(m.messages, message{role: roleUser, text: text})
	m.composer.SetValue("")
	m.composer.Blur()
	m.running = true
	m.job = jobContractDraft
	m.activity = "revising contract draft"
	m.notice = "Revising the proposed contract · Ply has not started"
	options := m.taskOptions
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.contractEvents = m.contracts.Compile(ctx, contractexec.DraftRequest{
		Task: plyexec.TaskRequest{
			Dir: m.workspace, Goal: current.Intent, Session: m.session, SubagentsDir: m.subagentsPath(),
			Skills: append([]string(nil), m.activeSkills...), Toolbox: m.toolbox, Model: m.modelName, Options: options,
		},
		Current: current, Instruction: text, Store: m.contractStore,
	})
	m.syncContent()
	return m, tea.Batch(waitContractDraftEvent(m.contractEvents), tick())
}

func (m *Model) editContract() (tea.Model, tea.Cmd) {
	if m.contractDraft == nil {
		if m.admittedContract == nil {
			m.notice = "No contract is available to edit"
			return m, nil
		}
		amendment := *m.admittedContract
		amendment.Generation++
		amendment.ParentRevisionID = amendment.RevisionID
		amendment.RevisionID = ""
		amendment.ContractID = ""
		var err error
		amendment, err = m.contractStore.SaveDraftCAS(amendment, m.admittedContract.DraftSHA256)
		if err != nil {
			m.notice = "Could not open an amendment · " + err.Error()
			return m, nil
		}
		m.contractDraft = &amendment
	}
	m.editingContract = true
	m.notice = "Opening editable contract JSON · admitted revisions remain immutable"
	m.syncContent()
	return m, m.openEditor(m.contractStore.DraftPath())
}

func (m *Model) reloadContractDraft() (tea.Model, tea.Cmd) {
	if m.contracts == nil {
		m.notice = "Contract edit cannot be imported · contract controller unavailable"
		return m, nil
	}
	m.running = true
	m.activity = "sealing manual contract revision"
	m.notice = "Recording manual contract revision…"
	m.syncContent()
	contracts, sessionPath, store := m.contracts, m.session, m.contractStore
	return m, func() tea.Msg {
		loaded, err := contracts.Import(context.Background(), contractexec.ImportRequest{Session: sessionPath, Store: store})
		return manualContractMsg{draft: loaded, err: err}
	}
}

func (m *Model) updateManualContract(msg manualContractMsg) (tea.Model, tea.Cmd) {
	m.running = false
	m.activity = ""
	if msg.err != nil {
		m.notice = "Manual contract revision was not sealed · " + msg.err.Error()
		m.syncContent()
		return m, m.composer.Focus()
	}
	draft := msg.draft
	previous := m.contractDraft
	if previous == nil {
		previous = m.admittedContract
	}
	m.contractDraft = &draft
	m.admittedContract = nil
	m.contractAudit = false
	m.messages = append(m.messages, message{role: roleContractDraft, text: contractDraftCard(draft, previous, m.contractStore.DraftPath(), m.taskOptions)})
	m.notice = fmt.Sprintf("Manual contract draft r%d validated and sealed", draft.Generation)
	m.syncContent()
	return m, m.composer.Focus()
}

func (m *Model) acceptContractDraft() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.composer.Value()) != "" {
		m.notice = "Revision text is not applied yet · press Enter to revise before admitting"
		return m, nil
	}
	if !m.taskOptions.IntentContract {
		m.notice = "Outcome contracts are off · use /contract on before admitting this draft"
		return m, nil
	}
	if m.contractDraft == nil {
		m.notice = "No editable contract draft is awaiting acceptance"
		return m, nil
	}
	loaded, status, err := m.contractStore.Load()
	if err != nil || status != "draft" {
		if err == nil {
			err = errors.New("contract is not in draft state")
		}
		m.notice = "Contract cannot be accepted · " + err.Error()
		return m, nil
	}
	shown := m.contractDraft.DraftSHA256
	if loaded.DraftSHA256 != shown {
		m.contractDraft = &loaded
		m.notice = "Contract changed since it was shown · review the newer draft before accepting"
		m.syncContent()
		return m, nil
	}
	contract, _, _, err := contractexec.Parse(string(loaded.Contract))
	if err != nil {
		m.notice = "Contract cannot be accepted · " + err.Error()
		return m, nil
	}
	if len(contract.OpenQuestions) > 0 || len(contract.Approvals) > 0 {
		m.notice = fmt.Sprintf("Contract still needs resolution · %d question(s), %d approval(s)", len(contract.OpenQuestions), len(contract.Approvals))
		return m, nil
	}
	if m.contracts == nil {
		m.notice = "Contract admission is unavailable"
		return m, nil
	}
	task := m.taskForContract(loaded, m.taskOptions)
	if err := plyexec.Validate(task); err != nil {
		m.notice = "Contract cannot start · " + err.Error()
		return m, nil
	}
	if err := m.armLoopSteering(task.Options); err != nil {
		m.notice = "Contract cannot start · " + err.Error()
		return m, nil
	}
	task.Steering = m.steeringPath
	m.taskOptions.Check = loaded.Check
	m.taskOptions.CheckAllCriteria = loaded.CheckAll
	m.contractDraft = &loaded
	m.running = true
	m.job = jobPlyTask
	m.activity = "admitting contract before work"
	m.notice = "Admitting the exact reviewed contract · Ply has not started yet"
	m.composer.SetValue("")
	m.composer.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.plyEvents = m.contracts.Admit(ctx, contractexec.AdmitRequest{
		Task:  task,
		Draft: loaded, Store: m.contractStore, ExpectedDraftSHA256: shown,
	})
	m.syncContent()
	return m, tea.Batch(waitPlyEvent(m.plyEvents), tick())
}

func (m *Model) runAdmittedContract(guidance string) (tea.Model, tea.Cmd) {
	if m.contracts == nil {
		m.notice = "Contract runner is unavailable"
		return m, nil
	}
	loaded, status, err := m.contractStore.Load()
	if err != nil || status != "admitted" {
		if err == nil {
			err = errors.New("contract is not admitted")
		}
		m.notice = "Contract cannot run · " + err.Error()
		return m, nil
	}
	task := m.taskForContract(loaded, m.taskOptions)
	if err := plyexec.Validate(task); err != nil {
		m.notice = "Contract cannot start · " + err.Error()
		return m, nil
	}
	if err := m.armLoopSteering(task.Options); err != nil {
		m.notice = "Contract cannot start · " + err.Error()
		return m, nil
	}
	task.Steering = m.steeringPath
	m.taskOptions.Check = loaded.Check
	m.taskOptions.CheckAllCriteria = loaded.CheckAll
	m.admittedContract = &loaded
	m.contractDraft = nil
	m.screen = screenAsk
	m.composer.SetValue("")
	m.composer.Blur()
	m.stdout.Reset()
	m.activity = "verifying admission before Ply starts"
	m.toolActivity = ""
	m.running = true
	m.job = jobPlyTask
	m.activeTaskIntent = loaded.Intent
	m.notice = "Rechecking the sealed admission · Ply starts only if it matches"
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.plyEvents = m.contracts.Run(ctx, contractexec.RunRequest{
		Task:  task,
		Draft: loaded, Store: m.contractStore, Guidance: strings.TrimSpace(guidance),
	})
	m.syncContent()
	return m, tea.Batch(waitPlyEvent(m.plyEvents), tick())
}

func (m *Model) taskForContract(draft contractexec.Draft, options plyexec.TaskOptions) plyexec.TaskRequest {
	options.IntentContract = true
	options.Check = draft.Check
	options.CheckAllCriteria = draft.CheckAll
	return plyexec.TaskRequest{
		Dir: m.workspace, Goal: draft.Intent, Session: m.session, SubagentsDir: m.subagentsPath(),
		Skills: append([]string(nil), draft.Skills...), Toolbox: draft.Toolbox, Model: m.modelName, Options: options,
	}
}

func (m *Model) openContract() (tea.Model, tea.Cmd) {
	if m.contractDraft == nil && m.admittedContract == nil {
		loaded, status, err := m.contractStore.Load()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				m.notice = "No durable contract exists for this session"
			} else {
				m.notice = "Contract state could not be opened · " + err.Error()
			}
			m.syncContent()
			return m, nil
		}
		if status == "draft" {
			m.contractDraft = &loaded
		} else {
			m.admittedContract = &loaded
		}
		m.taskOptions.Check = loaded.Check
		m.taskOptions.CheckAllCriteria = loaded.CheckAll
	}
	m.screen = screenContract
	m.composer.SetValue("")
	m.composer.Placeholder = "Describe an amendment, press e to edit, or /contract accept…"
	m.notice = "Contract opened from durable state"
	m.syncContent()
	return m, m.composer.Focus()
}

func (m *Model) restoreContractState() {
	loaded, status, err := m.contractStore.Load()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.notice = "Session verified, but durable contract state is unreadable · " + err.Error()
		}
		return
	}
	if status == "draft" {
		m.contractDraft = &loaded
		m.admittedContract = nil
		m.taskOptions.Check = loaded.Check
		m.taskOptions.CheckAllCriteria = loaded.CheckAll
		m.screen = screenContract
		m.notice = "Session verified · unadmitted contract draft restored · Ply has not started"
		return
	}
	m.admittedContract = &loaded
	m.contractDraft = nil
	m.taskOptions.Check = loaded.Check
	m.taskOptions.CheckAllCriteria = loaded.CheckAll
	m.notice = "Local admitted revision restored · its sealed admission is reverified before any retry"
}

func (m *Model) renderContract(width int) string {
	t := makeTheme(m.dark)
	draft := m.contractDraft
	status := "DRAFT · NOT ADMITTED"
	path := m.contractStore.DraftPath()
	if draft == nil {
		draft = m.admittedContract
		status = "ADMITTED · IMMUTABLE REVISION"
		path = "Use e or describe a change to create a child amendment."
	}
	if draft == nil {
		return t.muted.Render("No contract is loaded.")
	}
	contract, _, _, err := contractexec.Parse(string(draft.Contract))
	body := ""
	if err != nil {
		body = "Invalid contract: " + err.Error()
	} else {
		body = safeText(contractexec.RenderSummary(contract))
	}
	rows := []string{
		t.hero.Render("Outcome contract") + "  " + t.warning.Render(status),
		t.faint.Render(fmt.Sprintf("revision %d · press a for audit details", draft.Generation)),
		"", body, "", contractExecutionPolicy(*draft, m.taskOptions),
	}
	if m.contractAudit {
		rows = append(rows, "", t.faint.Render(ansi.Truncate(safeText(path), max(12, width-4), "…")), "", contractBindings(*draft))
	}
	if m.running {
		rows = append(rows, "", t.working.Render(spinnerFrame(m.spinner)+"  "+lastUsefulLine(m.activity)))
	}
	return strings.Join(rows, "\n")
}

func contractDraftCard(draft contractexec.Draft, previous *contractexec.Draft, path string, policies ...plyexec.TaskOptions) string {
	contract, _, _, err := contractexec.Parse(string(draft.Contract))
	if err != nil {
		return "CONTRACT DRAFT INVALID\n" + err.Error()
	}
	change := "Initial proposal"
	if previous != nil && draft.OutcomeID != "" && previous.OutcomeID == draft.OutcomeID {
		change = contractSemanticChanges(*previous, draft)
	}
	return fmt.Sprintf("DRAFT r%d · NOT ADMITTED\n%s\nEdit: press e in Contract Review\nAudit: press a\n\n%s\n\n%s", draft.Generation, change, safeText(contractexec.RenderSummary(contract)), contractExecutionPolicy(draft, policies...))
}

func contractSemanticChanges(before, after contractexec.Draft) string {
	a, _, _, aErr := contractexec.Parse(string(before.Contract))
	b, _, _, bErr := contractexec.Parse(string(after.Contract))
	if aErr != nil || bErr != nil {
		return "Replacement proposal"
	}
	changed := make([]string, 0, 9)
	if a.Outcome != b.Outcome {
		changed = append(changed, "outcome")
	}
	for _, field := range []struct {
		name string
		a, b []string
	}{
		{"deliverables", a.Deliverables, b.Deliverables}, {"guardrails", a.Invariants, b.Invariants},
		{"approvals", a.Approvals, b.Approvals}, {"assumptions", a.Assumptions, b.Assumptions},
		{"open decisions", a.OpenQuestions, b.OpenQuestions}, {"limits", a.Limits, b.Limits},
	} {
		if !slices.Equal(field.a, field.b) {
			changed = append(changed, field.name)
		}
	}
	if !slices.Equal(a.Criteria, b.Criteria) {
		changed = append(changed, "evidence")
	}
	if before.Intent != after.Intent || before.Workspace != after.Workspace || before.Toolbox != after.Toolbox || before.Check != after.Check || before.CheckAll != after.CheckAll || !slices.Equal(before.Skills, after.Skills) {
		changed = append(changed, "execution policy")
	}
	if len(changed) == 0 {
		return "No semantic change"
	}
	return "Changed: " + strings.Join(changed, ", ")
}

func contractBindings(draft contractexec.Draft) string {
	revision := "not admitted"
	if draft.RevisionID != "" {
		revision = draft.RevisionID
	}
	admission := "not admitted"
	if draft.ContractID != "" {
		admission = draft.ContractID
	}
	return safeText(fmt.Sprintf("AUDIT DETAILS\nExact draft: %s\nRecorded draft: %s\nOutcome lineage: %s\nRevision: %s\nAdmission: %s\nCanonical contract: %s\nCheck digest: %s\nCompiler evidence: %s",
		draft.DraftSHA256, draft.RecordedDraftSHA256, draft.OutcomeID, revision, admission, draft.ContractSHA256, draft.CheckSHA256, draft.CompilerEvidenceSHA256))
}

func contractExecutionPolicy(draft contractexec.Draft, policies ...plyexec.TaskOptions) string {
	tools := "full shell"
	if strings.TrimSpace(draft.Toolbox) != "" {
		tools = "toolbox " + draft.Toolbox
	}
	check := "none"
	if strings.TrimSpace(draft.Check) != "" {
		check = draft.Check
	}
	authority := "no executable verifier; every criterion requires review"
	if strings.TrimSpace(draft.Check) != "" {
		authority = "verifier gate only; criteria still require review"
	}
	if draft.CheckAll && strings.TrimSpace(draft.Check) != "" {
		authority = "operator-admitted judge for every criterion"
	} else if draft.CheckAll {
		authority = "invalid: check-all has no configured verifier"
	}
	skills := "none"
	if len(draft.Skills) > 0 {
		skills = strings.Join(draft.Skills, ", ")
	}
	pursuit := "review · one Ply invocation after admission"
	if len(policies) > 0 && policies[0].Loop {
		cycles := plyexec.LoopCycleBudget(policies[0])
		turns := plyexec.LoopTurnBudget(policies[0])
		pursuit = fmt.Sprintf("loop · this invocation · cycles=%s · turns=%s", cycles, turns)
	}
	return safeText(fmt.Sprintf("EXECUTION POLICY\nOriginal request: %s\nWorkspace: %s\nTools: %s\nCheck: %s\nCheck authority: %s\nBrief skills: %s\nPursuit: %s", draft.Intent, draft.Workspace, tools, check, authority, skills, pursuit))
}

func failureDetail(code int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("compiler exited %d", code)
}
