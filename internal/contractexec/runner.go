package contractexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type Runner struct {
	Ask askexec.Client
	Ply plyexec.Worker
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
	contractID := envelopeID(canonical, req.Goal, compilerEvidence, req.Options.Check, req.Skills)
	recordBody, err := json.Marshal(struct {
		Status       string          `json:"status"`
		Compiler     string          `json:"compiler"`
		ContractID   string          `json:"contract_id"`
		ContractSHA  string          `json:"contract_sha256"`
		IntentSHA256 string          `json:"intent_sha256"`
		EvidenceSHA  string          `json:"compiler_evidence_sha256"`
		CheckSHA     string          `json:"check_sha256"`
		Skills       []string        `json:"skills"`
		Contract     json.RawMessage `json:"contract"`
	}{
		Status: "compiled", Compiler: "bench-default",
		ContractID: contractID, ContractSHA: "sha256:" + contractDigest,
		IntentSHA256: sha256Text(req.Goal), EvidenceSHA: sha256Text(compilerEvidence),
		CheckSHA: sha256Text(req.Options.Check),
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
	digest := strings.TrimPrefix(contractID, "sha256:")
	emit(ctx, events, plyexec.Event{Contract: Render(contract, digest), ContractDigest: digest})
	if len(contract.OpenQuestions) > 0 || len(contract.Approvals) > 0 {
		result := pendingResult(contract, "sha256:"+digest, req.Options.Check != "", "needs_decision")
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

	work := req
	work.Options.ContractID = "sha256:" + digest
	work.Goal = workGoal(req.Goal, canonical, digest, req.Options.Check)
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
	result := aggregate(contract, "sha256:"+digest, req.Options.Check != "", *terminal)
	// The original request session is controller-owned input and already holds
	// the compiled contract. Ply's session-out path is worker-visible control
	// data; do not let it redirect an authoritative Bench record.
	if err := r.recordResult(ctx, req.Session, result); err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}
	for _, event := range heldStdout {
		emit(ctx, events, event)
	}
	// Do not adopt Ply's worker-visible session-out path as controller state.
	// Compaction lineage needs an independently verified protocol before Bench
	// may trust a successor for authoritative records or future turns.
	terminal.Session = req.Session
	terminal.ContractResult = &result
	if result.Status == "review_required" {
		terminal.ExitCode = 2
		terminal.Err = nil
		terminal.Stream = plyexec.Stderr
		terminal.Text = resultSummary(result)
	}
	emitFinal(ctx, events, *terminal)
}

func (r Runner) recordResult(ctx context.Context, session string, result plyexec.ContractResult) error {
	recordJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("record contract result: %w", err)
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.Ask.Record(recordCtx, askexec.RecordRequest{
		Session: session, Source: "bench", Kind: "bench.contract-result/v1", JSON: string(recordJSON),
	}); err != nil {
		return fmt.Errorf("record contract result: %w", err)
	}
	return nil
}

func pendingResult(contract Contract, contractID string, checkConfigured bool, status string) plyexec.ContractResult {
	result := plyexec.ContractResult{
		ContractID: contractID, Status: status, CheckConfigured: checkConfigured,
		ProposedCheckCoverage: []string{}, Outstanding: []plyexec.ContractCriterion{},
		OpenQuestions: append([]string{}, contract.OpenQuestions...), PendingApprovals: append([]string{}, contract.Approvals...),
	}
	for _, criterion := range contract.Criteria {
		result.Outstanding = append(result.Outstanding, plyexec.ContractCriterion{ID: criterion.ID, Judge: criterion.Judge})
	}
	return result
}

func aggregate(contract Contract, contractID string, checkConfigured bool, terminal plyexec.Event) plyexec.ContractResult {
	result := pendingResult(contract, contractID, checkConfigured, "")
	result.WorkerExitCode = terminal.ExitCode
	checkAccepted := checkConfigured && terminal.Err == nil && terminal.ExitCode == 0
	result.CheckPassed = checkAccepted
	for _, criterion := range contract.Criteria {
		if checkAccepted && criterion.Judge == "check" {
			result.ProposedCheckCoverage = append(result.ProposedCheckCoverage, criterion.ID)
		}
	}
	switch {
	case errors.Is(terminal.Err, context.Canceled):
		result.Status = "interrupted"
	case terminal.Err == nil && terminal.ExitCode == 0:
		result.Status = "review_required"
	case terminal.ExitCode == 2:
		result.Status = "not_done"
	default:
		result.Status = "failed"
	}
	return result
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

func envelopeID(contract, intent, evidence, check string, skills []string) string {
	body, _ := json.Marshal(struct {
		Version  int             `json:"version"`
		Contract json.RawMessage `json:"contract"`
		Intent   string          `json:"intent_sha256"`
		Evidence string          `json:"compiler_evidence_sha256"`
		Check    string          `json:"check_sha256"`
		Skills   []string        `json:"skills"`
	}{
		Version: 1, Contract: json.RawMessage(contract), Intent: sha256Text(intent),
		Evidence: sha256Text(evidence), Check: sha256Text(check), Skills: append([]string{}, skills...),
	})
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compilerMessage(req plyexec.TaskRequest) string {
	verifier := "No executable verifier is configured. Distinguish evidence the worker can gather from claims only a person can judge."
	if req.Options.Check != "" {
		verifier = "The operator configured this exact verifier. Use judge=check only for criteria it directly establishes, without treating it as broader proof:\n" + req.Options.Check
	}
	return "Compile this user intent into an outcome contract.\n\nUSER INTENT\n" + req.Goal + "\n\nVERIFIER BOUNDARY\n" + verifier
}

func workGoal(intent, contract, digest, check string) string {
	verifier := "No executable verifier is configured. You may stop after reporting evidence, but the contracted outcome will remain ready for review rather than complete."
	if check != "" {
		verifier = "The operator's fixed verifier decides only the criteria marked judge=check. Its exact command is:\n" + check
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
- Report inspection and human criteria as pending acceptance even when you gathered useful evidence.
- Approval boundaries remain boundaries: do not cross one without the required approval.
- Open questions block only the affected irreversible or materially ambiguous step; continue safe reversible work where possible.
- Do not silently weaken or amend this contract. If evidence makes it wrong or impossible, report the exact proposed amendment and why.
- %s
`, intent, Version, digest, contract, verifier)
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
