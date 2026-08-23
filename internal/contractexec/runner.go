package contractexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type Runner struct {
	Ask interface {
		askexec.Client
		askexec.AdmissionReader
	}
	Ply      plyexec.Worker
	MayPath  string
	CagePath string
}

func (r Runner) Work(ctx context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	if err := plyexec.Validate(req); err != nil {
		events := make(chan plyexec.Event, 1)
		events <- plyexec.Event{Done: true, ExitCode: 1, Err: err}
		close(events)
		return events
	}
	if !req.Options.IntentContract {
		return r.Ply.Work(ctx, req)
	}
	events := make(chan plyexec.Event, 1)
	events <- plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("contract work requires a durable proposal and explicit admission")}
	close(events)
	return events
}

// compileAndWork retains the old v2 producer only for compatibility tests of
// historic records. No public runtime path calls it; new contracted work must
// pass through Compile and Admit.
func (r Runner) compileAndWork(ctx context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	events := make(chan plyexec.Event, 16)
	go r.run(ctx, req, events)
	return events
}

func (r Runner) run(ctx context.Context, req plyexec.TaskRequest, events chan<- plyexec.Event) {
	defer close(events)
	if r.Ask == nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("intent compiler needs ask")})
		return
	}
	if r.Ply == nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("intent compiler needs ply")})
		return
	}
	message := compilerMessage(req)
	compilerEvidence := Evidence(req.Dir, req.Input)
	var answer strings.Builder
	var outcome askexec.Event
	for event := range r.Ask.Start(ctx, askexec.Request{
		Message: message,
		Input:   compilerEvidence,
		Session: req.Session,
		Model:   req.Model,
		Effort:  req.Options.Effort,
		System:  System,
		Schema:  Schema,
		Skills:  append([]string(nil), req.Skills...),
	}) {
		if event.Done {
			outcome = event
			continue
		}
		if event.Stream == askexec.Stdout {
			answer.WriteString(event.Text)
		} else {
			emit(ctx, events, plyexec.Event{Stream: plyexec.Stderr, Text: event.Text})
		}
	}
	if outcome.Err != nil || outcome.ExitCode != 0 {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err, Session: req.Session})
		return
	}
	contract, canonical, contractDigest, err := Parse(answer.String())
	if err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}
	contractID := envelopeID(canonical, req.Goal, compilerEvidence, req.Options.Check, req.Options.CheckAllCriteria, req.Skills)
	recordBody, err := json.Marshal(struct {
		Status       string          `json:"status"`
		Compiler     string          `json:"compiler"`
		ContractID   string          `json:"contract_id"`
		ContractSHA  string          `json:"contract_sha256"`
		IntentSHA256 string          `json:"intent_sha256"`
		EvidenceSHA  string          `json:"compiler_evidence_sha256"`
		CheckSHA     string          `json:"check_sha256"`
		CheckAll     bool            `json:"check_all"`
		Skills       []string        `json:"skills"`
		Contract     json.RawMessage `json:"contract"`
	}{
		Status: "compiled", Compiler: "bench-default",
		ContractID: contractID, ContractSHA: "sha256:" + contractDigest,
		IntentSHA256: sha256Text(req.Goal), EvidenceSHA: sha256Text(compilerEvidence),
		CheckSHA: sha256Text(req.Options.Check),
		CheckAll: req.Options.CheckAllCriteria,
		Skills:   append([]string{}, req.Skills...),
		Contract: json.RawMessage(canonical),
	})
	if err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}
	if err := r.Ask.Record(ctx, askexec.RecordRequest{
		Session: req.Session, Source: "bench", Kind: "bench.contract/v2", JSON: string(recordBody),
	}); err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("record outcome contract: %w", err), Session: req.Session})
		return
	}
	r.runAccepted(ctx, req, contract, canonical, contractDigest, contractID, events)
}

// runAccepted is the single transition from an admitted, immutable contract
// into workspace work.
func (r Runner) runAccepted(ctx context.Context, req plyexec.TaskRequest, contract Contract, canonical, contractDigest, contractID string, events chan<- plyexec.Event) {
	digest := strings.TrimPrefix(contractID, "sha256:")
	emit(ctx, events, plyexec.Event{Contract: renderWithSkills(contract, digest, req.Skills), ContractDigest: digest})
	if len(contract.OpenQuestions) > 0 || len(contract.Approvals) > 0 {
		result := pendingResult(contract, "sha256:"+digest, req.Options.Check != "", "needs_decision")
		if req.Options.ApprovalPolicy == plyexec.ApprovalEveryAction {
			result.ApprovalPolicy = plyexec.ApprovalEveryAction
		}
		if req.Options.ActionConfinement == plyexec.ConfinementCage {
			result.ActionConfinement = plyexec.ConfinementCage
		}
		result = withPursuit(result, req.Options)
		setStopReason(&result, req.Options, "needs_decision")
		if err := r.recordResult(ctx, req.Session, result); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
		emitFinal(ctx, events, plyexec.Event{
			Done: true, ExitCode: 2, Stream: plyexec.Stderr, Session: req.Session, ContractResult: &result,
			Text: fmt.Sprintf("Needs decision · %d open question(s) and %d approval(s) must be resolved before work begins · reply with the missing decision · session is replayable\n", len(contract.OpenQuestions), len(contract.Approvals)),
		})
		return
	}
	admitted, judgeMapSHA, err := r.admitJudge(ctx, req, contract, contractID, "sha256:"+contractDigest)
	if err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}

	work := req
	work.Options.ContractID = "sha256:" + digest
	work.Goal = workGoal(req.Goal, canonical, digest, req.Options.Check, req.Options.CheckAllCriteria)
	mayPath := ""
	maySHA256 := ""
	cagePath := ""
	cageSHA256 := ""
	receiptDirectory := req.Dir
	if req.Options.ApprovalPolicy == plyexec.ApprovalEveryAction {
		var err error
		receiptDirectory, err = canonicalApprovalDirectory(req.Dir)
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
	}
	if req.Options.ActionConfinement == plyexec.ConfinementCage {
		var err error
		cagePath, err = plyexec.ResolveCagePath(r.CagePath)
		if err == nil {
			cageSHA256, err = plyexec.ExecutableSHA256(cagePath)
		}
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
	}
	if req.Options.ApprovalPolicy == plyexec.ApprovalEveryAction {
		var err error
		mayPath, err = plyexec.ResolveMayPath(r.MayPath)
		if err == nil {
			maySHA256, err = plyexec.ExecutableSHA256(mayPath)
		}
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
	}
	var terminal *plyexec.Event
	var heldStdout []plyexec.Event
	for event := range r.Ply.Work(ctx, work) {
		// The contract turn already created the session. Ply intentionally does
		// not create one when a pre-check passes, so keep the real session
		// visible instead of claiming that no model turn occurred.
		if event.Done && event.Session == "" {
			event.Session = req.Session
		}
		if event.Done {
			copy := event
			terminal = &copy
			continue
		}
		if event.Stream == plyexec.Stdout {
			heldStdout = append(heldStdout, event)
			continue
		}
		emit(ctx, events, event)
	}
	if terminal == nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("ply ended without a terminal result"), Session: req.Session})
		return
	}
	var candidate strings.Builder
	for _, event := range heldStdout {
		candidate.WriteString(event.Text)
	}
	var receipt *plyexec.VerifierReceiptRef
	var receiptErr error
	var approvalReceipt *plyexec.ApprovalReceiptRef
	var approvalErr error
	if req.Options.ApprovalPolicy == plyexec.ApprovalEveryAction && !errors.Is(terminal.Err, context.Canceled) && (terminal.ExitCode == 75 || terminal.ExitCode == 3) {
		var got askexec.ApprovalReceipt
		var err error
		if cagePath != "" {
			reader, ok := r.Ask.(askexec.CagedApprovalReader)
			if !ok {
				approvalErr = errors.New("Ask adapter cannot read caged approval receipts")
			} else {
				got, err = reader.TerminalCagedApproval(ctx, req.Session, contractID, plyexec.MayJob(contractID), receiptDirectory, mayPath, maySHA256, cagePath, cageSHA256)
			}
		} else {
			reader, ok := r.Ask.(askexec.ApprovalReader)
			if !ok {
				approvalErr = errors.New("Ask adapter cannot read approval receipts")
			} else {
				got, err = reader.TerminalApproval(ctx, req.Session, contractID, plyexec.MayJob(contractID), receiptDirectory, mayPath, maySHA256)
			}
		}
		if approvalErr == nil {
			if err != nil {
				approvalErr = err
			} else if map[int]string{75: "parked", 3: "declined"}[terminal.ExitCode] != got.Verdict {
				approvalErr = errors.New("approval receipt verdict does not match Ply exit status")
			} else {
				approvalReceipt = &plyexec.ApprovalReceiptRef{
					Seq: got.Seq, BodySHA256: got.BodySHA256, SealSHA256: got.SealSHA256,
					Job: got.Job, Digest: got.Digest, Verdict: got.Verdict, Action: got.Action, ActionSHA256: got.ActionSHA256, MayPath: mayPath, MaySHA256: got.MaySHA256,
				}
			}
		}
	}
	var confinementReceipt *plyexec.ConfinementReceiptRef
	var confinementErr error
	if req.Options.ActionConfinement == plyexec.ConfinementCage && !errors.Is(terminal.Err, context.Canceled) && terminal.ExitCode == 125 {
		reader, ok := r.Ask.(askexec.ConfinementReader)
		if !ok {
			confinementErr = errors.New("Ask adapter cannot read confinement receipts")
		} else {
			got, err := reader.TerminalConfinement(ctx, req.Session, contractID, plyexec.MayJob(contractID), receiptDirectory, mayPath, maySHA256, cagePath, cageSHA256)
			if err != nil {
				confinementErr = err
			} else {
				confinementReceipt = &plyexec.ConfinementReceiptRef{Seq: got.Seq, BodySHA256: got.BodySHA256, SealSHA256: got.SealSHA256, ActionSHA256: got.ActionSHA256, MayHaveRun: got.MayHaveRun, Detail: got.Detail}
			}
		}
	}
	if req.Options.CheckAllCriteria && terminal.Err == nil && terminal.ExitCode == 0 {
		reader, ok := r.Ask.(askexec.VerifierReader)
		if !ok {
			receiptErr = errors.New("Ask adapter cannot read verifier receipts")
		} else {
			got, err := reader.AcceptedVerifier(ctx, req.Session, judgeMapSHA, contractID, req.Options.Check, verifierCandidateSHA(candidate.String()), receiptDirectory)
			if err != nil {
				receiptErr = err
			} else {
				receipt = &plyexec.VerifierReceiptRef{
					Seq: got.Seq, BodySHA256: got.BodySHA256, SealSHA256: got.SealSHA256, Phase: got.Phase,
					CandidateSHA256: got.CandidateSHA256, VerifierSHA256: got.VerifierSHA256,
				}
			}
		}
	}
	result := aggregate(contract, "sha256:"+digest, req.Options.Check != "", admitted, judgeMapSHA, receipt, req.Options, *terminal)
	result.ApprovalReceipt = approvalReceipt
	result.ConfinementReceipt = confinementReceipt
	if receiptErr != nil {
		result.Status = "failed"
		setStopReason(&result, req.Options, "verifier_receipt_unverified")
	}
	if approvalErr != nil {
		result.Status = "failed"
		result.ApprovalReceipt = nil
		result.StopReason = "approval_receipt_unverified"
	}
	if confinementErr != nil {
		result.Status = "failed"
		result.ConfinementReceipt = nil
		result.StopReason = "confinement_receipt_unverified"
	}
	// The original request session is controller-owned input and already holds
	// the compiled contract. Ply's session-out path is worker-visible control
	// data; do not let it redirect an authoritative Bench record.
	if err := r.recordResult(ctx, req.Session, result); err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}
	if receiptErr == nil && approvalErr == nil && confinementErr == nil && result.Status != "awaiting_approval" && result.Status != "approval_declined" && result.Status != "confinement_failed" {
		for _, event := range heldStdout {
			emit(ctx, events, event)
		}
	}
	// Do not adopt Ply's worker-visible session-out path as controller state.
	// Compaction lineage needs an independently verified protocol before Bench
	// may trust a successor for authoritative records or future turns.
	terminal.Session = req.Session
	terminal.ContractResult = &result
	switch result.Status {
	case "complete":
		terminal.ExitCode = 0
		terminal.Err = nil
		terminal.Stream = plyexec.Stderr
		terminal.Text = completeSummary(result)
	case "review_required":
		terminal.ExitCode = 2
		terminal.Err = nil
		terminal.Stream = plyexec.Stderr
		terminal.Text = resultSummary(result)
	case "failed":
		if receiptErr != nil || approvalErr != nil || confinementErr != nil {
			terminal.ExitCode = 1
			if confinementErr != nil {
				terminal.Err = fmt.Errorf("verify Ply confinement receipt: %w", confinementErr)
			} else if approvalErr != nil {
				terminal.Err = fmt.Errorf("verify Ply approval receipt: %w", approvalErr)
			} else {
				terminal.Err = fmt.Errorf("verify accepted Ply receipt: %w", receiptErr)
			}
			terminal.Stream = plyexec.Stderr
			terminal.Text = ""
		}
	case "awaiting_approval":
		terminal.ExitCode = 75
		terminal.Err = nil
		terminal.Stream = plyexec.Stderr
		terminal.Text = fmt.Sprintf("Approval required · action %s was not executed\nMay executable: %q\nDecision argv: decide %s\n", result.ApprovalReceipt.Digest, result.ApprovalReceipt.MayPath, result.ApprovalReceipt.Digest)
	case "approval_declined":
		terminal.ExitCode = 3
		terminal.Err = nil
		terminal.Stream = plyexec.Stderr
		terminal.Text = fmt.Sprintf("Approval declined · action %s was not executed\n", result.ApprovalReceipt.Digest)
	case "confinement_failed":
		terminal.ExitCode = 125
		terminal.Err = nil
		terminal.Stream = plyexec.Stderr
		if result.ConfinementReceipt.MayHaveRun {
			terminal.Text = "Confinement result was not accepted; the approved action may have run. No later model turn or verifier ran. Inspect the workspace and evidence before retrying.\n"
		} else {
			terminal.Text = "Cage could not establish the admitted confinement boundary; the action did not run. No later model turn or verifier ran.\n"
		}
	}
	emitFinal(ctx, events, *terminal)
}

func renderWithSkills(contract Contract, digest string, skills []string) string {
	text := Render(contract, digest)
	if len(skills) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\nBrief procedures shaping this outcome and its work:\n")
	for _, skill := range skills {
		fmt.Fprintf(&b, "- %s\n", skill)
	}
	b.WriteString("These procedures guide the work; they do not decide the verdict.")
	return b.String()
}

func (r Runner) recordResult(ctx context.Context, session string, result plyexec.ContractResult) error {
	recordJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("record contract result: %w", err)
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	kind := "bench.contract-result/v1"
	if result.ActionConfinement != "" {
		kind = "bench.contract-result/v5"
	} else if result.ApprovalPolicy != "" {
		kind = "bench.contract-result/v4"
	} else if result.Pursuit != "" {
		kind = "bench.contract-result/v3"
	} else if result.JudgeMapSHA256 != "" {
		kind = "bench.contract-result/v2"
	}
	if err := r.Ask.Record(recordCtx, askexec.RecordRequest{
		Session: session, Source: "bench", Kind: kind, JSON: string(recordJSON),
	}); err != nil {
		return fmt.Errorf("record contract result: %w", err)
	}
	return nil
}

func (r Runner) admitJudge(ctx context.Context, req plyexec.TaskRequest, contract Contract, contractID, contractSHA string) ([]string, string, error) {
	if !req.Options.CheckAllCriteria {
		return []string{}, "", nil
	}
	criteria := make([]string, 0, len(contract.Criteria))
	for _, criterion := range contract.Criteria {
		criteria = append(criteria, criterion.ID)
	}
	body, err := json.Marshal(struct {
		ContractID   string   `json:"contract_id"`
		ContractSHA  string   `json:"contract_sha256"`
		CheckSHA     string   `json:"check_sha256"`
		Workdir      string   `json:"workdir"`
		Policy       string   `json:"policy"`
		Authority    string   `json:"authority"`
		CriterionIDs []string `json:"criterion_ids"`
	}{
		ContractID: contractID, ContractSHA: contractSHA, CheckSHA: sha256Text(req.Options.Check),
		Workdir: req.Dir, Policy: "all", Authority: "operator-check-all", CriterionIDs: criteria,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode judge map: %w", err)
	}
	if err := r.Ask.Record(ctx, askexec.RecordRequest{
		Session: req.Session, Source: "bench", Kind: "bench.judge-map/v1", JSON: string(body),
	}); err != nil {
		return nil, "", fmt.Errorf("record judge map: %w", err)
	}
	return criteria, sha256Text(string(body)), nil
}

func pendingResult(contract Contract, contractID string, checkConfigured bool, status string) plyexec.ContractResult {
	result := plyexec.ContractResult{
		ContractID: contractID, Status: status, CheckConfigured: checkConfigured,
		ProposedCheckCoverage: []string{}, AdmittedCheckCoverage: []string{}, Outstanding: []plyexec.ContractCriterion{},
		OpenQuestions: append([]string{}, contract.OpenQuestions...), PendingApprovals: append([]string{}, contract.Approvals...),
	}
	for _, criterion := range contract.Criteria {
		result.Outstanding = append(result.Outstanding, plyexec.ContractCriterion{ID: criterion.ID, Judge: criterion.Judge})
	}
	return result
}

func aggregate(contract Contract, contractID string, checkConfigured bool, admitted []string, judgeMapSHA string, receipt *plyexec.VerifierReceiptRef, options plyexec.TaskOptions, terminal plyexec.Event) plyexec.ContractResult {
	result := pendingResult(contract, contractID, checkConfigured, "")
	result = withPursuit(result, options)
	result.WorkerExitCode = terminal.ExitCode
	result.JudgeMapSHA256 = judgeMapSHA
	result.VerifierReceipt = receipt
	if options.ApprovalPolicy == plyexec.ApprovalEveryAction {
		result.ApprovalPolicy = plyexec.ApprovalEveryAction
	}
	if options.ActionConfinement == plyexec.ConfinementCage {
		result.ActionConfinement = plyexec.ConfinementCage
	}
	checkAccepted := checkConfigured && terminal.Err == nil && terminal.ExitCode == 0
	if judgeMapSHA != "" {
		checkAccepted = checkAccepted && receipt != nil
	}
	result.CheckPassed = checkAccepted
	for _, criterion := range contract.Criteria {
		if checkAccepted && criterion.Judge == "check" {
			result.ProposedCheckCoverage = append(result.ProposedCheckCoverage, criterion.ID)
		}
	}
	if checkAccepted && receipt != nil {
		accepted := make(map[string]bool, len(admitted))
		for _, id := range admitted {
			accepted[id] = true
			result.AdmittedCheckCoverage = append(result.AdmittedCheckCoverage, id)
		}
		pending := result.Outstanding[:0]
		for _, criterion := range result.Outstanding {
			if !accepted[criterion.ID] {
				pending = append(pending, criterion)
			}
		}
		result.Outstanding = pending
	}
	switch {
	case options.ApprovalPolicy == plyexec.ApprovalEveryAction && !errors.Is(terminal.Err, context.Canceled) && terminal.ExitCode == 75:
		result.Status = "awaiting_approval"
		result.StopReason = "approval_required"
	case options.ApprovalPolicy == plyexec.ApprovalEveryAction && !errors.Is(terminal.Err, context.Canceled) && terminal.ExitCode == 3:
		result.Status = "approval_declined"
		result.StopReason = "approval_declined"
	case options.ActionConfinement == plyexec.ConfinementCage && !errors.Is(terminal.Err, context.Canceled) && terminal.ExitCode == 125:
		result.Status = "confinement_failed"
		result.StopReason = "confinement_failed"
	case errors.Is(terminal.Err, context.Canceled):
		result.Status = "interrupted"
		setStopReason(&result, options, "interrupted")
	case terminal.Err == nil && terminal.ExitCode == 0 && len(result.Outstanding) == 0 && receipt != nil:
		result.Status = "complete"
		setStopReason(&result, options, "verifier_accepted")
	case terminal.Err == nil && terminal.ExitCode == 0:
		result.Status = "review_required"
		setStopReason(&result, options, "verifier_accepted_review_required")
	case terminal.ExitCode == 2:
		result.Status = "not_done"
		setStopReason(&result, options, "ply_not_done")
	default:
		result.Status = "failed"
		setStopReason(&result, options, "ply_failed")
	}
	return result
}

func canonicalApprovalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve approval workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve approval workspace: %w", err)
	}
	return resolved, nil
}

func setStopReason(result *plyexec.ContractResult, options plyexec.TaskOptions, reason string) {
	if options.Loop {
		result.StopReason = reason
	}
}

func withPursuit(result plyexec.ContractResult, options plyexec.TaskOptions) plyexec.ContractResult {
	if !options.Loop {
		return result
	}
	result.Pursuit = "loop-this-invocation"
	result.CycleBudget = plyexec.LoopCycleBudget(options)
	result.TurnBudget = plyexec.LoopTurnBudget(options)
	return result
}

func completeSummary(result plyexec.ContractResult) string {
	total := len(result.AdmittedCheckCoverage)
	return fmt.Sprintf("Outcome complete · operator-admitted check passed %d/%d criteria · session is replayable\n", total, total)
}

func resultSummary(result plyexec.ContractResult) string {
	total := len(result.Outstanding)
	otherJudge := 0
	for _, criterion := range result.Outstanding {
		if criterion.Judge != "check" {
			otherJudge++
		}
	}
	questions := ""
	if len(result.OpenQuestions) > 0 {
		questions = fmt.Sprintf(" · %d open question(s) require a decision", len(result.OpenQuestions))
	}
	if result.CheckConfigured {
		return fmt.Sprintf("Ready for review · configured check passed · proposed coverage %d/%d criteria · %d remain unaccepted · %d require inspection/human review%s · session is replayable\n",
			len(result.ProposedCheckCoverage), total, total, otherJudge, questions)
	}
	return fmt.Sprintf("Ready for review · no configured check · %d/%d criteria require review%s · session is replayable\n",
		len(result.Outstanding), total, questions)
}

func sha256Text(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Ply terminates a non-empty report with exactly one newline before sending
// it to the verifier. Bind the receipt lookup to those exact stdin bytes.
func verifierCandidateSHA(candidate string) string {
	if candidate == "" {
		return sha256Text("")
	}
	return sha256Text(strings.TrimRight(candidate, "\n") + "\n")
}

func envelopeID(contract, intent, evidence, check string, checkAll bool, skills []string) string {
	return envelopeIDFromDigests(contract, sha256Text(intent), sha256Text(evidence), sha256Text(check), checkAll, skills)
}

func envelopeIDFromDigests(contract, intentSHA, evidenceSHA, checkSHA string, checkAll bool, skills []string) string {
	body, _ := json.Marshal(struct {
		Version  int             `json:"version"`
		Contract json.RawMessage `json:"contract"`
		Intent   string          `json:"intent_sha256"`
		Evidence string          `json:"compiler_evidence_sha256"`
		Check    string          `json:"check_sha256"`
		CheckAll bool            `json:"check_all"`
		Skills   []string        `json:"skills"`
	}{
		Version: 1, Contract: json.RawMessage(contract), Intent: intentSHA,
		Evidence: evidenceSHA, Check: checkSHA, CheckAll: checkAll, Skills: append([]string{}, skills...),
	})
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compilerMessage(req plyexec.TaskRequest) string {
	verifier := "No executable verifier is configured. Distinguish evidence the worker can gather from claims only a person can judge."
	if req.Options.Check != "" {
		verifier = "The operator configured this exact verifier. Use judge=check only for criteria it directly establishes, without treating it as broader proof:\n" + req.Options.Check
		if req.Options.CheckAllCriteria {
			verifier = "The operator explicitly admits this exact verifier as judge of every contract criterion. Still classify evidence honestly for explanation; the operator policy, never your labels, supplies authority:\n" + req.Options.Check
		}
	}
	approval := approvalCompilerBoundary(req.Options.ApprovalPolicy)
	confinement := confinementCompilerBoundary(req.Options.ActionConfinement)
	return "Compile this user intent into an outcome contract.\n\nUSER INTENT\n" + req.Goal + "\n\nACTION APPROVAL POLICY\n" + approval + "\n\nACTION CONFINEMENT\n" + confinement + "\n\nVERIFIER BOUNDARY\n" + verifier
}

func approvalCompilerBoundary(policy string) string {
	if policy == plyexec.ApprovalEveryAction {
		return "every-action — contract approvals authorize preparation only; every exact model action still requires a separate May decision before execution"
	}
	return "off — an explicit answer to a contract approval authorizes the described consequential scope; there is no execution-time May gate"
}

func workGoal(intent, contract, digest, check string, checkAll bool) string {
	verifier := "No executable verifier is configured. You may stop after reporting evidence, but the contracted outcome will remain ready for review rather than complete."
	inspectionRule := "Report inspection and human criteria as pending acceptance even when you gathered useful evidence."
	if check != "" {
		verifier = "The operator's fixed verifier decides only the criteria marked judge=check. Its exact command is:\n" + check
		if checkAll {
			verifier = "The operator explicitly admitted the fixed verifier as judge of every criterion. Its exact command is:\n" + check
			inspectionRule = "Treat model-assigned judge labels as explanatory; the operator-admitted check decides every criterion."
		}
	}
	return fmt.Sprintf(`Pursue the user's outcome under this compiled contract.

ORIGINAL INTENT
%s

OUTCOME CONTRACT v%d
sha256 %s
%s

WORKING RULES
- Address every deliverable, invariant, and acceptance criterion.
- Gather the named evidence; do not replace evidence with a claim that work is done.
- %s
- Approval boundaries remain boundaries: do not cross one without the required approval.
- Open questions block only the affected irreversible or materially ambiguous step; continue safe reversible work where possible.
- Do not silently weaken or amend this contract. If evidence makes it wrong or impossible, report the exact proposed amendment and why.
- %s
`, intent, Version, digest, contract, inspectionRule, verifier)
}

func emit(ctx context.Context, dst chan<- plyexec.Event, event plyexec.Event) {
	select {
	case dst <- event:
	case <-ctx.Done():
	}
}

func emitFinal(ctx context.Context, dst chan<- plyexec.Event, event plyexec.Event) {
	select {
	case dst <- event:
		return
	default:
	}
	select {
	case dst <- event:
	case <-ctx.Done():
		select {
		case dst <- event:
		default:
		}
	}
}
