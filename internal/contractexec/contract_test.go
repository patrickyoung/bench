package contractexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

const fixtureContract = `{
  "version": 2,
  "outcome": "A complete gallery exists in the workspace.",
  "deliverables": ["poem-gallery.html containing both poems and distinct ASCII scenes"],
  "invariants": ["Source poems remain byte-for-byte unchanged"],
  "criteria": [
    {"id":"fidelity","requirement":"Every source line is present","evidence":"Exact comparison against both sources","judge":"check"},
    {"id":"layout","requirement":"Desktop and mobile layouts remain legible","evidence":"Rendered inspection at two viewport widths","judge":"inspection"}
  ],
  "approvals": [],
  "assumptions": ["Create one self-contained HTML file"],
  "open_questions": [],
  "limits": ["Visual appeal remains a human judgment"]
}`

func TestParseCanonicalizesAndNamesExactContract(t *testing.T) {
	c, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	if c.Outcome == "" || len(digest) != 64 || !strings.Contains(canonical, `"judge": "inspection"`) {
		t.Fatalf("contract=%#v digest=%q canonical=%s", c, digest, canonical)
	}
	rendered := Render(c, digest)
	for _, want := range []string{"OUTCOME CONTRACT v2", "Acceptance evidence:", "[check]", "mobile layouts", "Assumptions:", "Create one self-contained HTML file", "Limits:", "Visual appeal remains a human judgment"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestContractSchemaUsesStrictProviderSubset(t *testing.T) {
	for _, want := range []string{
		`"version": {"type": "integer", "const": 2}`,
		`"judge": {"type": "string", "enum": ["check", "inspection", "human"]}`,
	} {
		if !strings.Contains(Schema, want) {
			t.Errorf("schema missing explicit type for strict provider: %s", want)
		}
	}
}

func TestContractEnvelopeBindsIntentEvidenceCheckAndSkillRefs(t *testing.T) {
	_, canonical, _, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	base := envelopeID(canonical, "intent", "inventory and stdin", "test -s out", false, []string{"web", "house"})
	variants := []string{
		envelopeID(canonical, "changed intent", "inventory and stdin", "test -s out", false, []string{"web", "house"}),
		envelopeID(canonical, "intent", "changed evidence", "test -s out", false, []string{"web", "house"}),
		envelopeID(canonical, "intent", "inventory and stdin", "true", false, []string{"web", "house"}),
		envelopeID(canonical, "intent", "inventory and stdin", "test -s out", true, []string{"web", "house"}),
		envelopeID(canonical, "intent", "inventory and stdin", "test -s out", false, []string{"house", "web"}),
	}
	for i, got := range variants {
		if got == base {
			t.Errorf("variant %d did not change contract envelope id", i)
		}
	}
}

func TestParseRejectsIncompleteOrDuplicateCriteria(t *testing.T) {
	bad := strings.Replace(fixtureContract, `"id":"layout"`, `"id":"fidelity"`, 1)
	if _, _, _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("duplicate criterion error=%v", err)
	}
	bad = strings.Replace(fixtureContract, `"outcome": "A complete gallery exists in the workspace."`, `"outcome": " "`, 1)
	if _, _, _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "no outcome") {
		t.Fatalf("empty outcome error=%v", err)
	}
	bad = strings.Replace(fixtureContract, `"limits": [`, `"surprise": true, "limits": [`, 1)
	if _, _, _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error=%v", err)
	}
}

func TestEvidenceIsReadOnlyBoundedAndExcludesBenchState(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"poem.md", "src/main.go", ".bench/sessions/private.jsonl", ".git/config"} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Evidence(dir, "piped facts")
	for _, want := range []string{"poem.md", "src/", "src/main.go", "USER-SUPPLIED INPUT", "piped facts"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "private.jsonl") || strings.Contains(got, ".git/config") {
		t.Fatalf("evidence exposed ignored state:\n%s", got)
	}
}

type fakeAsk struct {
	req              askexec.Request
	record           askexec.RecordRequest
	recordLog        []askexec.RecordRequest
	answer           string
	answers          []string
	reqLog           []askexec.Request
	exit             int
	err              error
	recordErr        error
	recordErrAt      int
	calls            int
	records          int
	receipt          askexec.VerifierReceipt
	receiptErr       error
	receiptCalls     int
	approval         askexec.ApprovalReceipt
	approvalErr      error
	approvalCalls    int
	approvalDir      string
	approvalMay      string
	approvalSHA      string
	confinement      askexec.ConfinementReceipt
	confinementErr   error
	confinementCalls int
	admissionErr     error
	admissions       int
}

func (f *fakeAsk) TerminalConfinement(_ context.Context, _, _, _, _, _, _, _, _ string) (askexec.ConfinementReceipt, error) {
	f.confinementCalls++
	if f.confinementErr != nil {
		return askexec.ConfinementReceipt{}, f.confinementErr
	}
	if f.confinement.BodySHA256 != "" {
		return f.confinement, nil
	}
	return askexec.ConfinementReceipt{Seq: 11, ExitCode: 125, BodySHA256: "sha256:confinement", SealSHA256: "sha256:seal", ActionSHA256: "sha256:action", MayHaveRun: true, Detail: "reserved child status"}, nil
}

func (f *fakeAsk) TerminalApproval(_ context.Context, _, contractID, job, directory, mayPath, maySHA256 string) (askexec.ApprovalReceipt, error) {
	f.approvalCalls++
	f.approvalDir, f.approvalMay, f.approvalSHA = directory, mayPath, maySHA256
	if f.approvalErr != nil {
		return askexec.ApprovalReceipt{}, f.approvalErr
	}
	if f.approval.BodySHA256 != "" {
		return f.approval, nil
	}
	return askexec.ApprovalReceipt{
		Seq: 9, BodySHA256: "sha256:approval", SealSHA256: "sha256:seal", ContractID: contractID,
		Job: job, Digest: strings.Repeat("a", 64), Verdict: "parked", Action: "{}\n", ActionSHA256: "sha256:action", MaySHA256: maySHA256,
	}, nil
}

func (f *fakeAsk) AdmittedContract(_ context.Context, _ string, _ askexec.AdmissionExpectation) error {
	f.admissions++
	return f.admissionErr
}

func (f *fakeAsk) AcceptedVerifier(_ context.Context, _, _, contractID, verifier, candidateSHA, _ string) (askexec.VerifierReceipt, error) {
	f.receiptCalls++
	if f.receiptErr != nil {
		return askexec.VerifierReceipt{}, f.receiptErr
	}
	if f.receipt.BodySHA256 != "" {
		return f.receipt, nil
	}
	return askexec.VerifierReceipt{
		Seq: 7, BodySHA256: "sha256:receipt", SealSHA256: "sha256:seal", ContractID: contractID,
		Phase: "candidate", CandidateSHA256: candidateSHA, Verifier: verifier,
		VerifierSHA256: "sha256:verifier", Outcome: "accepted", ExitCode: 0,
	}, nil
}

func (f *fakeAsk) Record(_ context.Context, req askexec.RecordRequest) error {
	f.record, f.records = req, f.records+1
	f.recordLog = append(f.recordLog, req)
	if f.recordErr != nil && (f.recordErrAt == 0 || f.recordErrAt == f.records) {
		return f.recordErr
	}
	return nil
}

func (f *fakeAsk) Start(_ context.Context, req askexec.Request) <-chan askexec.Event {
	f.req, f.calls = req, f.calls+1
	f.reqLog = append(f.reqLog, req)
	events := make(chan askexec.Event, 3)
	answer := f.answer
	if len(f.answers) >= f.calls {
		answer = f.answers[f.calls-1]
	}
	if answer != "" {
		events <- askexec.Event{Stream: askexec.Stdout, Text: answer}
	}
	events <- askexec.Event{Done: true, ExitCode: f.exit, Err: f.err}
	close(events)
	return events
}

type fakePly struct {
	req    plyexec.TaskRequest
	event  plyexec.Event
	events []plyexec.Event
	calls  int
}

func (f *fakePly) Work(_ context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	f.req, f.calls = req, f.calls+1
	stream := make(chan plyexec.Event, len(f.events)+1)
	if len(f.events) > 0 {
		for _, event := range f.events {
			stream <- event
		}
	} else {
		stream <- f.event
	}
	close(stream)
	return stream
}

func TestRunnerCompilesThenWorksInOneSession(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poem.md"), []byte("verse"), 0o644); err != nil {
		t.Fatal(err)
	}
	ask := &fakeAsk{answer: fixtureContract}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	req := plyexec.TaskRequest{
		Dir: dir, Goal: "make art", Input: "two poems", Session: filepath.Join(dir, "session.jsonl"),
		Model: "openai/luna", Skills: []string{"web-quality"},
		Options: plyexec.TaskOptions{IntentContract: true, Effort: "xhigh", Check: "test -s poem-gallery.html"},
	}
	var contract, digest string
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), req) {
		if event.Contract != "" {
			contract, digest = event.Contract, event.ContractDigest
		}
		if event.Done {
			done = event
		}
	}
	if ask.calls != 1 || ask.records != 2 || ply.calls != 1 || contract == "" || len(digest) != 64 {
		t.Fatalf("ask=%d records=%d ply=%d contract=%q digest=%q", ask.calls, ask.records, ply.calls, contract, digest)
	}
	for _, want := range []string{"Brief procedures shaping this outcome and its work", "- web-quality", "do not decide the verdict"} {
		if !strings.Contains(contract, want) {
			t.Errorf("visible contract hid procedure %q:\n%s", want, contract)
		}
	}
	if ask.req.Session != req.Session || ply.req.Session != req.Session || ask.req.System != System || ask.req.Schema != Schema || len(ask.req.Skills) != 1 || ask.req.Skills[0] != "web-quality" {
		t.Fatalf("ask=%#v ply=%#v", ask.req, ply.req)
	}
	for _, want := range []string{"make art", "test -s poem-gallery.html"} {
		if !strings.Contains(ask.req.Message, want) {
			t.Errorf("compiler message missing %q:\n%s", want, ask.req.Message)
		}
	}
	for _, want := range []string{"poem.md", "two poems"} {
		if !strings.Contains(ask.req.Input, want) {
			t.Errorf("compiler evidence missing %q:\n%s", want, ask.req.Input)
		}
	}
	for _, want := range []string{"ORIGINAL INTENT\nmake art", "OUTCOME CONTRACT v2", digest, `"outcome": "A complete gallery`, "fixed verifier decides only"} {
		if !strings.Contains(ply.req.Goal, want) {
			t.Errorf("work goal missing %q:\n%s", want, ply.req.Goal)
		}
	}
	compiled := ask.recordLog[0]
	if compiled.Session != req.Session || compiled.Source != "bench" || compiled.Kind != "bench.contract/v2" {
		t.Fatalf("contract record = %#v", compiled)
	}
	for _, want := range []string{`"status":"compiled"`, `"compiler":"bench-default"`, `"contract_id":"sha256:` + digest, `"contract_sha256":"sha256:`, `"intent_sha256":"sha256:`, `"compiler_evidence_sha256":"sha256:`, `"check_sha256":"sha256:`, `"skills":["web-quality"]`} {
		if !strings.Contains(compiled.JSON, want) {
			t.Errorf("contract record missing %q: %s", want, compiled.JSON)
		}
	}
	result := ask.recordLog[1]
	if result.Kind != "bench.contract-result/v1" || !strings.Contains(result.JSON, `"status":"review_required"`) || !strings.Contains(result.JSON, `"proposed_check_coverage":["fidelity"]`) || !strings.Contains(result.JSON, `"id":"layout"`) {
		t.Fatalf("contract result = %#v", result)
	}
	if ply.req.Options.ContractID != "sha256:"+digest {
		t.Fatalf("Ply contract id = %q", ply.req.Options.ContractID)
	}
	if ply.req.Options.Check != req.Options.Check {
		t.Fatalf("skill/compiler changed configured check: got %q want %q", ply.req.Options.Check, req.Options.Check)
	}
	if done.Session != req.Session || done.ExitCode != 2 || done.ContractResult == nil || done.ContractResult.Status != "review_required" {
		t.Fatalf("passing precheck lost contract session: %#v", done)
	}
}

func TestRunnerDoesNotWorkWhenContractRecordCannotBeSealed(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract, recordErr: errors.New("disk full")}
	ply := &fakePly{}
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 1 || done.Err == nil || !strings.Contains(done.Err.Error(), "record outcome contract") || ply.calls != 0 {
		t.Fatalf("done=%#v ply calls=%d", done, ply.calls)
	}
}

func TestRunnerKeepsAllModelProposedCheckCoveragePending(t *testing.T) {
	allCheck := strings.Replace(fixtureContract, `"judge":"inspection"`, `"judge":"check"`, 1)
	ask := &fakeAsk{answer: allCheck}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Check: "./check"},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 2 || done.ContractResult == nil || done.ContractResult.Status != "review_required" || len(done.ContractResult.ProposedCheckCoverage) != 2 || len(done.ContractResult.Outstanding) != 2 {
		t.Fatalf("done=%#v", done)
	}
	if ask.records != 2 || !strings.Contains(ask.recordLog[1].JSON, `"status":"review_required"`) || strings.Contains(ask.recordLog[1].JSON, `"status":"complete"`) {
		t.Fatalf("records=%#v", ask.recordLog)
	}
}

func TestOperatorCheckAllCompletesEveryCriterionFromMatchedReceipt(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract}
	ply := &fakePly{events: []plyexec.Event{{Stream: plyexec.Stdout, Text: "finished\n\n"}, {Done: true, ExitCode: 0}}}
	req := plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Check: "./check", CheckAllCriteria: true},
	}
	var done plyexec.Event
	var stdout strings.Builder
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), req) {
		if event.Stream == plyexec.Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 0 || done.Err != nil || done.ContractResult == nil || done.ContractResult.Status != "complete" {
		t.Fatalf("done=%#v", done)
	}
	if stdout.String() != "finished\n\n" || ask.receiptCalls != 1 || len(done.ContractResult.AdmittedCheckCoverage) != 2 || len(done.ContractResult.Outstanding) != 0 || done.ContractResult.VerifierReceipt == nil {
		t.Fatalf("stdout=%q receipts=%d result=%#v", stdout.String(), ask.receiptCalls, done.ContractResult)
	}
	if got := ask.receipt.CandidateSHA256; got != "" {
		t.Fatalf("fixture receipt unexpectedly overrode lookup: %#v", ask.receipt)
	}
	if len(ask.recordLog) != 3 {
		t.Fatalf("records=%#v", ask.recordLog)
	}
	wantKinds := []string{"bench.contract/v2", "bench.judge-map/v1", "bench.contract-result/v2"}
	for i, want := range wantKinds {
		if ask.recordLog[i].Kind != want {
			t.Fatalf("record %d kind=%q want %q", i, ask.recordLog[i].Kind, want)
		}
	}
	for _, want := range []string{`"policy":"all"`, `"authority":"operator-check-all"`, `"criterion_ids":["fidelity","layout"]`} {
		if !strings.Contains(ask.recordLog[1].JSON, want) {
			t.Errorf("judge map missing %q: %s", want, ask.recordLog[1].JSON)
		}
	}
	for _, want := range []string{`"status":"complete"`, `"admitted_check_coverage":["fidelity","layout"]`, `"outstanding":[]`, `"verifier_receipt":{`} {
		if !strings.Contains(ask.recordLog[2].JSON, want) {
			t.Errorf("result missing %q: %s", want, ask.recordLog[2].JSON)
		}
	}
}

func TestOperatorCheckAllNeverCompletesWithoutAcceptedReceipt(t *testing.T) {
	tests := []struct {
		name       string
		terminal   plyexec.Event
		receiptErr error
		wantStatus string
		wantExit   int
		wantCalls  int
	}{
		{name: "check rejected", terminal: plyexec.Event{Done: true, ExitCode: 2}, wantStatus: "not_done", wantExit: 2},
		{name: "receipt mismatch", terminal: plyexec.Event{Done: true, ExitCode: 0}, receiptErr: errors.New("receipt mismatch"), wantStatus: "failed", wantExit: 1, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ask := &fakeAsk{answer: fixtureContract, receiptErr: tt.receiptErr}
			ply := &fakePly{events: []plyexec.Event{{Stream: plyexec.Stdout, Text: "unsealed answer"}, tt.terminal}}
			var done plyexec.Event
			var stdout strings.Builder
			for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
				Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
				Options: plyexec.TaskOptions{IntentContract: true, Check: "./check", CheckAllCriteria: true},
			}) {
				if event.Stream == plyexec.Stdout {
					stdout.WriteString(event.Text)
				}
				if event.Done {
					done = event
				}
			}
			if done.ExitCode != tt.wantExit || done.ContractResult == nil || done.ContractResult.Status != tt.wantStatus || done.ContractResult.CheckPassed || len(done.ContractResult.Outstanding) != 2 || len(done.ContractResult.AdmittedCheckCoverage) != 0 || ask.receiptCalls != tt.wantCalls {
				t.Fatalf("done=%#v receipt calls=%d", done, ask.receiptCalls)
			}
			if tt.receiptErr != nil && stdout.Len() != 0 {
				t.Fatalf("receipt failure released stdout %q", stdout.String())
			}
			if ask.recordLog[len(ask.recordLog)-1].Kind != "bench.contract-result/v2" || strings.Contains(ask.recordLog[len(ask.recordLog)-1].JSON, `"status":"complete"`) || !strings.Contains(ask.recordLog[len(ask.recordLog)-1].JSON, `"check_passed":false`) {
				t.Fatalf("records=%#v", ask.recordLog)
			}
		})
	}
}

func TestLoopReceiptMismatchUsesV3AndFailsClosed(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract, receiptErr: errors.New("receipt mismatch")}
	ply := &fakePly{events: []plyexec.Event{{Stream: plyexec.Stdout, Text: "unsealed answer"}, {Done: true, ExitCode: 0}}}
	var done plyexec.Event
	var stdout strings.Builder
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Loop: true, Check: "./check", CheckAllCriteria: true},
	}) {
		if event.Stream == plyexec.Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	result := ask.recordLog[len(ask.recordLog)-1]
	if done.ExitCode != 1 || done.ContractResult == nil || done.ContractResult.Status != "failed" || done.ContractResult.CheckPassed || done.ContractResult.StopReason != "verifier_receipt_unverified" || stdout.Len() != 0 || result.Kind != "bench.contract-result/v3" {
		t.Fatalf("done=%#v stdout=%q record=%#v", done, stdout.String(), result)
	}
}

func TestLoopUsesResultV3WithoutChangingReviewResultSchemas(t *testing.T) {
	for _, test := range []struct {
		name    string
		options plyexec.TaskOptions
		want    string
	}{
		{name: "review", options: plyexec.TaskOptions{IntentContract: true, Check: "./check"}, want: "bench.contract-result/v1"},
		{name: "review check all", options: plyexec.TaskOptions{IntentContract: true, Check: "./check", CheckAllCriteria: true}, want: "bench.contract-result/v2"},
		{name: "loop", options: plyexec.TaskOptions{IntentContract: true, Loop: true, Check: "./check"}, want: "bench.contract-result/v3"},
		{name: "loop check all", options: plyexec.TaskOptions{IntentContract: true, Loop: true, Check: "./check", CheckAllCriteria: true}, want: "bench.contract-result/v3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ask := &fakeAsk{answer: fixtureContract}
			ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
			var terminal plyexec.Event
			for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
				Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"), Options: test.options,
			}) {
				if event.Done {
					terminal = event
				}
			}
			result := ask.recordLog[len(ask.recordLog)-1]
			if result.Kind != test.want {
				t.Fatalf("kind=%q want %q records=%#v", result.Kind, test.want, ask.recordLog)
			}
			if test.options.Loop {
				if !strings.Contains(result.JSON, `"pursuit":"loop-this-invocation"`) || !strings.Contains(result.JSON, `"stop_reason":`) {
					t.Fatalf("v3 result omitted loop policy: %s", result.JSON)
				}
			} else if strings.Contains(result.JSON, `"pursuit":`) || strings.Contains(result.JSON, `"cycle_budget":`) || strings.Contains(result.JSON, `"turn_budget":`) || strings.Contains(result.JSON, `"stop_reason":`) {
				t.Fatalf("legacy result schema changed: %s", result.JSON)
			}
			if test.name == "loop check all" && (terminal.ExitCode != 0 || terminal.ContractResult == nil || terminal.ContractResult.Status != "complete" || terminal.ContractResult.VerifierReceipt == nil) {
				t.Fatalf("loop v3 did not preserve receipt-backed completion: %#v", terminal)
			}
		})
	}
}

func TestEveryActionTerminalApprovalRequiresReceiptAndUsesResultV4(t *testing.T) {
	contract, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, verdict, status string
		exit                  int
	}{
		{name: "parked", verdict: "parked", status: "awaiting_approval", exit: 75},
		{name: "declined", verdict: "declined", status: "approval_declined", exit: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			ask := &fakeAsk{approval: askexec.ApprovalReceipt{
				Seq: 9, BodySHA256: "sha256:approval", SealSHA256: "sha256:seal",
				ContractID: "sha256:contract", Job: plyexec.MayJob("sha256:contract"),
				Digest: strings.Repeat("a", 64), Verdict: test.verdict, Action: "{}\n", ActionSHA256: "sha256:action",
			}}
			ply := &fakePly{events: []plyexec.Event{
				{Stream: plyexec.Stdout, Text: "must stay held"},
				{Done: true, ExitCode: test.exit, Err: fmt.Errorf("exit status %d", test.exit)},
			}}
			req := plyexec.TaskRequest{
				Dir: workspace, Goal: "make it", Session: "/sessions/run.jsonl",
				Options: plyexec.TaskOptions{IntentContract: true, ApprovalPolicy: plyexec.ApprovalEveryAction},
			}
			events := make(chan plyexec.Event, 8)
			go func() {
				(Runner{Ask: ask, Ply: ply, MayPath: "/usr/bin/true"}).runAccepted(context.Background(), req, contract, canonical, digest, "sha256:contract", events)
				close(events)
			}()
			var terminal plyexec.Event
			for event := range events {
				if !event.Done && event.Stream == plyexec.Stdout {
					t.Fatal("approval terminal released worker stdout")
				}
				if event.Done {
					terminal = event
				}
			}
			if terminal.ExitCode != test.exit || terminal.ContractResult == nil || terminal.ContractResult.Status != test.status || terminal.ContractResult.ApprovalReceipt == nil || ask.approvalCalls != 1 {
				t.Fatalf("terminal=%#v approval calls=%d", terminal, ask.approvalCalls)
			}
			if got := ask.recordLog[len(ask.recordLog)-1]; got.Kind != "bench.contract-result/v4" || !strings.Contains(got.JSON, `"approval_policy":"every-action"`) {
				t.Fatalf("result record=%#v", got)
			}
		})
	}
}

func TestCagedConfinementFailureRequiresReceiptAndUsesResultV5(t *testing.T) {
	contract, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	may := filepath.Join(t.TempDir(), "may")
	cage := filepath.Join(t.TempDir(), "cage")
	for _, path := range []string{may, cage} {
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ask := &fakeAsk{confinement: askexec.ConfinementReceipt{Seq: 11, ExitCode: 125, BodySHA256: "sha256:confinement", SealSHA256: "sha256:seal", ActionSHA256: "sha256:action", MayHaveRun: true, Detail: "child returned 125"}}
	ply := &fakePly{events: []plyexec.Event{{Stream: plyexec.Stdout, Text: "held"}, {Done: true, ExitCode: 125, Err: errors.New("exit status 125")}}}
	events := make(chan plyexec.Event, 8)
	go func() {
		(Runner{Ask: ask, Ply: ply, MayPath: may, CagePath: cage}).runAccepted(context.Background(), plyexec.TaskRequest{Dir: workspace, Goal: "make it", Session: "/sessions/run.jsonl", Options: plyexec.TaskOptions{IntentContract: true, ApprovalPolicy: plyexec.ApprovalEveryAction, ActionConfinement: plyexec.ConfinementCage}}, contract, canonical, digest, "sha256:contract", events)
		close(events)
	}()
	terminal := collectPly(t, events)
	if terminal.ExitCode != 125 || terminal.ContractResult == nil || terminal.ContractResult.Status != "confinement_failed" || terminal.ContractResult.ConfinementReceipt == nil || !terminal.ContractResult.ConfinementReceipt.MayHaveRun || ask.confinementCalls != 1 {
		t.Fatalf("terminal=%#v calls=%d", terminal, ask.confinementCalls)
	}
	if got := ask.recordLog[len(ask.recordLog)-1]; got.Kind != "bench.contract-result/v5" || !strings.Contains(got.JSON, `"action_confinement":"cage"`) {
		t.Fatalf("record=%#v", got)
	}
}

func TestCagedConfinementResultSealFailureFailsBeforeExposingExit125(t *testing.T) {
	contract, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	may := filepath.Join(t.TempDir(), "may")
	cage := filepath.Join(t.TempDir(), "cage")
	for _, path := range []string{may, cage} {
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ask := &fakeAsk{recordErr: errors.New("seal failed"), confinement: askexec.ConfinementReceipt{Seq: 11, ExitCode: 125, BodySHA256: "sha256:confinement", SealSHA256: "sha256:seal", ActionSHA256: "sha256:action", Detail: "setup failed"}}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 125, Err: errors.New("exit status 125")}}
	events := make(chan plyexec.Event, 8)
	go func() {
		(Runner{Ask: ask, Ply: ply, MayPath: may, CagePath: cage}).runAccepted(context.Background(), plyexec.TaskRequest{Dir: workspace, Goal: "make it", Session: "/sessions/run.jsonl", Options: plyexec.TaskOptions{IntentContract: true, ApprovalPolicy: plyexec.ApprovalEveryAction, ActionConfinement: plyexec.ConfinementCage}}, contract, canonical, digest, "sha256:contract", events)
		close(events)
	}()
	terminal := collectPly(t, events)
	if terminal.ExitCode != 1 || terminal.Err == nil || terminal.ContractResult != nil {
		t.Fatalf("terminal=%#v", terminal)
	}
}

func TestEveryActionTerminalApprovalFailsClosedWithoutMatchingReceipt(t *testing.T) {
	contract, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	ask := &fakeAsk{approvalErr: errors.New("receipt mismatch")}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 75}}
	req := plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "make it", Session: "/sessions/run.jsonl",
		Options: plyexec.TaskOptions{IntentContract: true, ApprovalPolicy: plyexec.ApprovalEveryAction},
	}
	events := make(chan plyexec.Event, 8)
	go func() {
		(Runner{Ask: ask, Ply: ply, MayPath: "/usr/bin/true"}).runAccepted(context.Background(), req, contract, canonical, digest, "sha256:contract", events)
		close(events)
	}()
	terminal := collectPly(t, events)
	if terminal.ExitCode != 1 || terminal.ContractResult == nil || terminal.ContractResult.Status != "failed" || terminal.ContractResult.ApprovalReceipt != nil || !strings.Contains(terminal.Err.Error(), "approval receipt") {
		t.Fatalf("terminal=%#v", terminal)
	}
}

func TestEveryActionTerminalExitMustMatchApprovalVerdict(t *testing.T) {
	contract, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		exit    int
		verdict string
	}{{exit: 75, verdict: "declined"}, {exit: 3, verdict: "parked"}} {
		workspace := t.TempDir()
		ask := &fakeAsk{approval: askexec.ApprovalReceipt{
			Seq: 9, BodySHA256: "sha256:approval", SealSHA256: "sha256:seal", ContractID: "sha256:contract",
			Job: plyexec.MayJob("sha256:contract"), Digest: strings.Repeat("a", 64), Verdict: test.verdict,
			Action: "{}\n", ActionSHA256: "sha256:action",
		}}
		ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: test.exit, Err: fmt.Errorf("exit status %d", test.exit)}}
		events := make(chan plyexec.Event, 8)
		go func() {
			(Runner{Ask: ask, Ply: ply, MayPath: "/usr/bin/true"}).runAccepted(context.Background(), plyexec.TaskRequest{
				Dir: workspace, Goal: "make it", Session: "/sessions/run.jsonl",
				Options: plyexec.TaskOptions{IntentContract: true, ApprovalPolicy: plyexec.ApprovalEveryAction},
			}, contract, canonical, digest, "sha256:contract", events)
			close(events)
		}()
		terminal := collectPly(t, events)
		if terminal.ExitCode != 1 || terminal.ContractResult == nil || terminal.ContractResult.Status != "failed" || !strings.Contains(terminal.Err.Error(), "verdict") {
			t.Fatalf("exit=%d verdict=%s terminal=%#v", test.exit, test.verdict, terminal)
		}
	}
}

func TestApprovalReceiptUsesCanonicalWorkspaceAndControllerResolvedMay(t *testing.T) {
	contract, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	wantTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	may := filepath.Join(t.TempDir(), "may with spaces")
	if err := os.WriteFile(may, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relMay, err := filepath.Rel(wd, may)
	if err != nil {
		t.Fatal(err)
	}
	ask := &fakeAsk{approval: askexec.ApprovalReceipt{
		Seq: 9, BodySHA256: "sha256:approval", SealSHA256: "sha256:seal", ContractID: "sha256:contract",
		Job: plyexec.MayJob("sha256:contract"), Digest: strings.Repeat("a", 64), Verdict: "parked", Action: "{}\n", ActionSHA256: "sha256:action",
	}}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 75, Err: errors.New("exit status 75")}}
	events := make(chan plyexec.Event, 8)
	go func() {
		(Runner{Ask: ask, Ply: ply, MayPath: relMay}).runAccepted(context.Background(), plyexec.TaskRequest{
			Dir: link, Goal: "make it", Session: "/sessions/run.jsonl",
			Options: plyexec.TaskOptions{IntentContract: true, ApprovalPolicy: plyexec.ApprovalEveryAction},
		}, contract, canonical, digest, "sha256:contract", events)
		close(events)
	}()
	terminal := collectPly(t, events)
	if terminal.ExitCode != 75 || ask.approvalDir != wantTarget || ask.approvalMay != may || !strings.HasPrefix(ask.approvalSHA, "sha256:") {
		t.Fatalf("terminal=%#v dir=%q may=%q sha=%q", terminal, ask.approvalDir, ask.approvalMay, ask.approvalSHA)
	}
	if !strings.Contains(terminal.Text, fmt.Sprintf("May executable: %q", may)) || !strings.Contains(terminal.Text, "Decision argv: decide ") {
		t.Fatalf("approval instructions are not literal-path safe: %q", terminal.Text)
	}
}

func TestCompilerExplainsApprovalAuthorityForBothPolicies(t *testing.T) {
	off := compilerMessage(plyexec.TaskRequest{Goal: "publish it"})
	gated := compilerMessage(plyexec.TaskRequest{Goal: "publish it", Options: plyexec.TaskOptions{ApprovalPolicy: plyexec.ApprovalEveryAction}})
	if !strings.Contains(off, "authorizes the described consequential scope") || !strings.Contains(off, "no execution-time May gate") {
		t.Fatalf("off policy was ambiguous:\n%s", off)
	}
	if !strings.Contains(gated, "preparation only") || !strings.Contains(gated, "separate May decision") {
		t.Fatalf("every-action policy was ambiguous:\n%s", gated)
	}
}

func TestCompilerTreatsVerifierImplementationAsOpaque(t *testing.T) {
	check := "'/operator/oracle.sh' check 'l02'"
	current := Draft{Intent: "repair it", Contract: []byte(fixtureContract)}
	ordinary := []string{
		compilerMessage(plyexec.TaskRequest{Goal: "repair it", Options: plyexec.TaskOptions{Check: check}}),
		revisionMessage(current, "keep the scope", check, false, plyexec.ApprovalOff),
	}
	checkAll := []string{
		compilerMessage(plyexec.TaskRequest{Goal: "repair it", Options: plyexec.TaskOptions{Check: check, CheckAllCriteria: true}}),
		revisionMessage(current, "keep the scope", check, true, plyexec.ApprovalOff),
	}
	messages := append(append([]string{}, ordinary...), checkAll...)
	for i, message := range messages {
		for _, want := range []string{check, "does not reveal its implementation", "Never infer internal tests", "describe only that a passing receipt exists for the exact verifier"} {
			if !strings.Contains(message, want) {
				t.Errorf("message %d missing %q:\n%s", i, want, message)
			}
		}
	}
	for i, message := range ordinary {
		if !strings.Contains(message, "assigns no criterion coverage or completion authority") || !strings.Contains(message, "remain pending") || strings.Contains(message, "operator assigned every contract criterion") {
			t.Errorf("ordinary message %d blurred authority:\n%s", i, message)
		}
	}
	for i, message := range checkAll {
		if !strings.Contains(message, "operator assigned every contract criterion") || strings.Contains(message, "assigns no criterion coverage") {
			t.Errorf("check-all message %d blurred authority:\n%s", i, message)
		}
	}
	for _, want := range []string{"command's text identifies the command", "Never infer internal tests", "separate read-only operator evidence", "ordinary configured check assigns no criterion coverage"} {
		if !strings.Contains(System, want) {
			t.Errorf("compiler system missing %q:\n%s", want, System)
		}
	}
}

func TestAggregateRecordsInvocationScopedLoopPolicy(t *testing.T) {
	result := aggregate(Contract{Criteria: []Criterion{{ID: "tests", Judge: "check"}}},
		"sha256:test", true, nil, "", nil,
		plyexec.TaskOptions{IntentContract: true, Loop: true, Check: "go test ./..."},
		plyexec.Event{Done: true, ExitCode: 2})
	if result.Status != "not_done" || result.Pursuit != "loop-this-invocation" || result.CycleBudget != "unbounded" || result.TurnBudget != "50" || result.StopReason != "ply_not_done" {
		t.Fatalf("result=%#v", result)
	}
	explicit := withPursuit(plyexec.ContractResult{}, plyexec.TaskOptions{Loop: true, Cycles: 3, HasCycles: true, Turns: 12, HasTurns: true})
	if explicit.CycleBudget != "3" || explicit.TurnBudget != "12" {
		t.Fatalf("explicit policy=%#v", explicit)
	}
	explicitZero := withPursuit(plyexec.ContractResult{}, plyexec.TaskOptions{Loop: true, Cycles: 0, HasCycles: true})
	if explicitZero.CycleBudget != "unbounded" || explicitZero.TurnBudget != "50" {
		t.Fatalf("explicit zero policy=%#v", explicitZero)
	}
	ordinary := withPursuit(plyexec.ContractResult{}, plyexec.TaskOptions{})
	if ordinary.Pursuit != "" || ordinary.CycleBudget != "" || ordinary.TurnBudget != "" || ordinary.StopReason != "" {
		t.Fatalf("review result grew loop policy=%#v", ordinary)
	}
}

func TestOperatorCheckAllJudgeMapMustSealBeforeWork(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract, recordErr: errors.New("disk full"), recordErrAt: 2}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Check: "true", CheckAllCriteria: true},
	}) {
		if event.Done {
			done = event
		}
	}
	if ply.calls != 0 || done.ExitCode != 1 || done.Err == nil || !strings.Contains(done.Err.Error(), "record judge map") {
		t.Fatalf("ply=%d done=%#v records=%#v", ply.calls, done, ask.recordLog)
	}
}

func TestVerifierCandidateSHAUsesPlysExactStdinNormalization(t *testing.T) {
	if got, want := verifierCandidateSHA("report\n\n"), sha256Text("report\n"); got != want {
		t.Fatalf("candidate digest=%q want %q", got, want)
	}
	if got, want := verifierCandidateSHA(""), sha256Text(""); got != want {
		t.Fatalf("empty candidate digest=%q want %q", got, want)
	}
}

func TestCheckAllWorkGoalDoesNotContradictOperatorAdmission(t *testing.T) {
	goal := workGoal("make art", fixtureContract, "digest", "./check", true)
	if strings.Contains(goal, "pending acceptance") || !strings.Contains(goal, "operator-admitted check decides every criterion") {
		t.Fatalf("check-all work goal contradicts admission:\n%s", goal)
	}
	ordinary := workGoal("make art", fixtureContract, "digest", "./check", false)
	if !strings.Contains(ordinary, "pending acceptance") || strings.Contains(ordinary, "operator-admitted check decides every criterion") {
		t.Fatalf("ordinary work goal lost review boundary:\n%s", ordinary)
	}
}

func TestResultSummarySurfacesOpenQuestions(t *testing.T) {
	result := aggregate(Contract{
		Criteria:      []Criterion{{ID: "exists", Judge: "check"}},
		OpenQuestions: []string{"Which printer should receive the document?"},
	}, "sha256:test", true, nil, "", nil, plyexec.TaskOptions{}, plyexec.Event{Done: true, ExitCode: 0})
	if result.Status != "review_required" || len(result.OpenQuestions) != 1 {
		t.Fatalf("result=%#v", result)
	}
	summary := resultSummary(result)
	if !strings.Contains(summary, "1 open question(s) require a decision") || strings.Contains(strings.ToLower(summary), "complete") {
		t.Fatalf("summary=%q", summary)
	}
}

func TestRunnerLetsWorkProceedWithoutAuthoritativeCoverage(t *testing.T) {
	tests := []struct {
		name, contract, check string
	}{
		{"check proposal without check", fixtureContract, ""},
		{"check without proposal", strings.Replace(fixtureContract, `"judge":"check"`, `"judge":"inspection"`, 1), "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ask := &fakeAsk{answer: tt.contract}
			ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
			var done plyexec.Event
			for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
				Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
				Options: plyexec.TaskOptions{IntentContract: true, Check: tt.check},
			}) {
				if event.Done {
					done = event
				}
			}
			if done.ExitCode != 2 || done.Err != nil || done.ContractResult == nil || done.ContractResult.Status != "review_required" || ply.calls != 1 || ask.records != 2 {
				t.Fatalf("done=%#v ply=%d records=%d", done, ply.calls, ask.records)
			}
		})
	}
}

func TestRunnerFailsClosedWhenContractResultCannotBeSealed(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract, recordErr: errors.New("disk full"), recordErrAt: 2}
	ply := &fakePly{events: []plyexec.Event{{Stream: plyexec.Stdout, Text: "apparent success"}, {Done: true, ExitCode: 0}}}
	var done plyexec.Event
	var stdout strings.Builder
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	}) {
		if event.Stream == plyexec.Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 1 || done.Err == nil || !strings.Contains(done.Err.Error(), "record contract result") || done.ContractResult != nil || stdout.Len() != 0 {
		t.Fatalf("done=%#v stdout=%q", done, stdout.String())
	}
}

func TestRunnerDoesNotLetWorkerRedirectAuthoritativeResultSession(t *testing.T) {
	successor := filepath.Join(t.TempDir(), "successor.jsonl")
	source := filepath.Join(t.TempDir(), "source.jsonl")
	ask := &fakeAsk{answer: fixtureContract}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0, Session: successor}}
	for range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: source,
		Options: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	}) {
	}
	if ask.records != 2 || ask.recordLog[1].Session != source {
		t.Fatalf("records=%#v", ask.recordLog)
	}
}

func TestRunnerPausesBeforeWorkForOpenQuestions(t *testing.T) {
	question := strings.Replace(fixtureContract, `"open_questions": []`, `"open_questions": ["Which printer should receive it?"]`, 1)
	ask := &fakeAsk{answer: question}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "source.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	}) {
		if event.Done {
			done = event
		}
	}
	if ply.calls != 0 || done.ExitCode != 2 || done.ContractResult == nil || done.ContractResult.Status != "needs_decision" || len(done.ContractResult.OpenQuestions) != 1 || !strings.Contains(done.Text, "before work begins") {
		t.Fatalf("ply=%d done=%#v", ply.calls, done)
	}
	if ask.records != 2 || !strings.Contains(ask.recordLog[1].JSON, `"status":"needs_decision"`) {
		t.Fatalf("records=%#v", ask.recordLog)
	}
}

func TestRunnerPausesBeforeWorkForPendingApproval(t *testing.T) {
	approval := strings.Replace(fixtureContract, `"approvals": []`, `"approvals": ["Send the document to an external printer"]`, 1)
	ask := &fakeAsk{answer: approval}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "print it", Session: filepath.Join(t.TempDir(), "source.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true},
	}) {
		if event.Done {
			done = event
		}
	}
	if ply.calls != 0 || done.ContractResult == nil || done.ContractResult.Status != "needs_decision" || len(done.ContractResult.PendingApprovals) != 1 || !strings.Contains(done.Text, "1 approval(s)") {
		t.Fatalf("ply=%d done=%#v", ply.calls, done)
	}
}

func TestDecisionThenResolvedContractRecordsInOrderBeforeWork(t *testing.T) {
	question := strings.Replace(fixtureContract, `"open_questions": []`, `"open_questions": ["Which printer?"]`, 1)
	ask := &fakeAsk{answers: []string{question, fixtureContract}}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	runner := Runner{Ask: ask, Ply: ply}
	session := filepath.Join(t.TempDir(), "source.jsonl")
	for range runner.compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "Write, save, and print a poem", Session: session,
		Options: plyexec.TaskOptions{IntentContract: true},
	}) {
	}
	resolved := "ORIGINAL USER INTENT\nWrite, save, and print a poem\n\nOPEN QUESTIONS\n- Which printer?\n\nUSER DECISION\nOffice printer"
	for range runner.compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: resolved, Session: session,
		Options: plyexec.TaskOptions{IntentContract: true},
	}) {
	}
	if ask.calls != 2 || ply.calls != 1 || len(ask.recordLog) != 4 {
		t.Fatalf("ask=%d ply=%d records=%#v", ask.calls, ply.calls, ask.recordLog)
	}
	wantKinds := []string{"bench.contract/v2", "bench.contract-result/v1", "bench.contract/v2", "bench.contract-result/v1"}
	for i, want := range wantKinds {
		if ask.recordLog[i].Kind != want {
			t.Fatalf("record %d kind=%q want %q", i, ask.recordLog[i].Kind, want)
		}
	}
	if !strings.Contains(ask.reqLog[1].Message, "Write, save, and print a poem") || !strings.Contains(ask.reqLog[1].Message, "Which printer?") || !strings.Contains(ask.reqLog[1].Message, "Office printer") {
		t.Fatalf("resolved compiler request:\n%s", ask.reqLog[1].Message)
	}
	if !strings.Contains(ask.recordLog[1].JSON, `"status":"needs_decision"`) || !strings.Contains(ask.recordLog[3].JSON, `"status":"review_required"`) {
		t.Fatalf("records=%#v", ask.recordLog)
	}
}

func TestRunnerNeverStartsWorkWithInvalidContract(t *testing.T) {
	ask := &fakeAsk{answer: `{"version":1}`}
	ply := &fakePly{}
	var done plyexec.Event
	for event := range (Runner{Ask: ask, Ply: ply}).compileAndWork(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
		Options: plyexec.TaskOptions{IntentContract: true},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 1 || done.Err == nil || ply.calls != 0 {
		t.Fatalf("done=%#v ply calls=%d", done, ply.calls)
	}
}

func TestRunnerCanUseDirectCompatibilityPath(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	for range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "work", Session: filepath.Join(t.TempDir(), "run.jsonl"),
	}) {
	}
	if ask.calls != 0 || ply.calls != 1 || ply.req.Goal != "work" {
		t.Fatalf("ask=%d ply=%d goal=%q", ask.calls, ply.calls, ply.req.Goal)
	}
}
