package contractexec

import (
	"context"
	"errors"
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
	for _, want := range []string{"OUTCOME CONTRACT v2", "Acceptance evidence:", "[check]", "mobile layouts"} {
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
	base := envelopeID(canonical, "intent", "inventory and stdin", "test -s out", []string{"web", "house"})
	variants := []string{
		envelopeID(canonical, "changed intent", "inventory and stdin", "test -s out", []string{"web", "house"}),
		envelopeID(canonical, "intent", "changed evidence", "test -s out", []string{"web", "house"}),
		envelopeID(canonical, "intent", "inventory and stdin", "true", []string{"web", "house"}),
		envelopeID(canonical, "intent", "inventory and stdin", "test -s out", []string{"house", "web"}),
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
	req         askexec.Request
	record      askexec.RecordRequest
	recordLog   []askexec.RecordRequest
	answer      string
	answers     []string
	reqLog      []askexec.Request
	exit        int
	err         error
	recordErr   error
	recordErrAt int
	calls       int
	records     int
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), req) {
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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

func TestResultSummarySurfacesOpenQuestions(t *testing.T) {
	result := aggregate(Contract{
		Criteria:      []Criterion{{ID: "exists", Judge: "check"}},
		OpenQuestions: []string{"Which printer should receive the document?"},
	}, "sha256:test", true, plyexec.Event{Done: true, ExitCode: 0})
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
			for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
	for range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
	for range runner.Work(context.Background(), plyexec.TaskRequest{
		Dir: t.TempDir(), Goal: "Write, save, and print a poem", Session: session,
		Options: plyexec.TaskOptions{IntentContract: true},
	}) {
	}
	resolved := "ORIGINAL USER INTENT\nWrite, save, and print a poem\n\nOPEN QUESTIONS\n- Which printer?\n\nUSER DECISION\nOffice printer"
	for range runner.Work(context.Background(), plyexec.TaskRequest{
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
	for event := range (Runner{Ask: ask, Ply: ply}).Work(context.Background(), plyexec.TaskRequest{
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
