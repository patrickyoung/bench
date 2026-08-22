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
  "version": 1,
  "outcome": "A complete gallery exists in the workspace.",
  "deliverables": ["poem-gallery.html containing both poems and distinct ASCII scenes"],
  "invariants": ["Source poems remain byte-for-byte unchanged"],
  "criteria": [
    {"id":"fidelity","requirement":"Every source line is present","evidence":"Exact comparison against both sources","judge":"executable"},
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
	for _, want := range []string{"OUTCOME CONTRACT v1", "Done means:", "[executable]", "mobile layouts"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestContractSchemaUsesStrictProviderSubset(t *testing.T) {
	for _, want := range []string{
		`"version": {"type": "integer", "const": 1}`,
		`"judge": {"type": "string", "enum": ["executable", "inspection", "human"]}`,
	} {
		if !strings.Contains(Schema, want) {
			t.Errorf("schema missing explicit type for strict provider: %s", want)
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
	req       askexec.Request
	record    askexec.RecordRequest
	answer    string
	exit      int
	err       error
	recordErr error
	calls     int
	records   int
}

func (f *fakeAsk) Record(_ context.Context, req askexec.RecordRequest) error {
	f.record, f.records = req, f.records+1
	return f.recordErr
}

func (f *fakeAsk) Start(_ context.Context, req askexec.Request) <-chan askexec.Event {
	f.req, f.calls = req, f.calls+1
	events := make(chan askexec.Event, 3)
	if f.answer != "" {
		events <- askexec.Event{Stream: askexec.Stdout, Text: f.answer}
	}
	events <- askexec.Event{Done: true, ExitCode: f.exit, Err: f.err}
	close(events)
	return events
}

type fakePly struct {
	req   plyexec.TaskRequest
	event plyexec.Event
	calls int
}

func (f *fakePly) Work(_ context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	f.req, f.calls = req, f.calls+1
	events := make(chan plyexec.Event, 1)
	events <- f.event
	close(events)
	return events
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
		Model: "openai/luna", Options: plyexec.TaskOptions{IntentContract: true, Effort: "xhigh", Check: "test -s poem-gallery.html"},
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
	if ask.calls != 1 || ask.records != 1 || ply.calls != 1 || contract == "" || len(digest) != 64 {
		t.Fatalf("ask=%d records=%d ply=%d contract=%q digest=%q", ask.calls, ask.records, ply.calls, contract, digest)
	}
	if ask.req.Session != req.Session || ply.req.Session != req.Session || ask.req.System != System || ask.req.Schema != Schema {
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
	for _, want := range []string{"ORIGINAL INTENT\nmake art", "OUTCOME CONTRACT v1", digest, `"outcome": "A complete gallery`, "fixed verifier still decides executable completion"} {
		if !strings.Contains(ply.req.Goal, want) {
			t.Errorf("work goal missing %q:\n%s", want, ply.req.Goal)
		}
	}
	if ask.record.Session != req.Session || ask.record.Source != "bench" || ask.record.Kind != "bench.contract/v1" {
		t.Fatalf("contract record = %#v", ask.record)
	}
	for _, want := range []string{`"status":"admitted"`, `"admission":"bench-default"`, `"contract_id":"sha256:` + digest, `"intent_sha256":"sha256:`} {
		if !strings.Contains(ask.record.JSON, want) {
			t.Errorf("contract record missing %q: %s", want, ask.record.JSON)
		}
	}
	if ply.req.Options.ContractID != "sha256:"+digest {
		t.Fatalf("Ply contract id = %q", ply.req.Options.ContractID)
	}
	if done.Session != req.Session {
		t.Fatalf("passing precheck lost contract session: %#v", done)
	}
}

func TestRunnerDoesNotWorkWhenContractRecordCannotBeSealed(t *testing.T) {
	ask := &fakeAsk{answer: fixtureContract, recordErr: errors.New("disk full")}
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
	if done.ExitCode != 1 || done.Err == nil || !strings.Contains(done.Err.Error(), "record outcome contract") || ply.calls != 0 {
		t.Fatalf("done=%#v ply calls=%d", done, ply.calls)
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
