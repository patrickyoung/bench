package ui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/autonomy"
	"github.com/patrickyoung/bench/internal/contractexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
)

type fakeRunner struct {
	events       chan askexec.Event
	replayEvents chan askexec.Event
	req          askexec.Request
	replayPath   string
}

type fakeTask struct {
	events   chan plyexec.Event
	req      plyexec.TaskRequest
	requests []plyexec.TaskRequest
	calls    int
}

type fakeNegotiator struct {
	compileEvents chan contractexec.DraftEvent
	plyEvents     chan plyexec.Event
	compileReqs   []contractexec.DraftRequest
	admitReqs     []contractexec.AdmitRequest
	runReqs       []contractexec.RunRequest
	importReqs    []contractexec.ImportRequest
	importDraft   contractexec.Draft
	importErr     error
}

func (f *fakeNegotiator) Import(_ context.Context, req contractexec.ImportRequest) (contractexec.Draft, error) {
	f.importReqs = append(f.importReqs, req)
	return f.importDraft, f.importErr
}

func (f *fakeNegotiator) Compile(_ context.Context, req contractexec.DraftRequest) <-chan contractexec.DraftEvent {
	f.compileReqs = append(f.compileReqs, req)
	if f.compileEvents == nil {
		f.compileEvents = make(chan contractexec.DraftEvent)
	}
	return f.compileEvents
}

func (f *fakeNegotiator) Admit(_ context.Context, req contractexec.AdmitRequest) <-chan plyexec.Event {
	f.admitReqs = append(f.admitReqs, req)
	if f.plyEvents == nil {
		f.plyEvents = make(chan plyexec.Event)
	}
	return f.plyEvents
}

func (f *fakeNegotiator) Run(_ context.Context, req contractexec.RunRequest) <-chan plyexec.Event {
	f.runReqs = append(f.runReqs, req)
	if f.plyEvents == nil {
		f.plyEvents = make(chan plyexec.Event)
	}
	return f.plyEvents
}

func (f *fakeTask) Work(_ context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	f.req = req
	f.requests = append(f.requests, req)
	f.calls++
	return f.events
}

func armContractResultPresentation(m *Model) {
	m.running = true
	m.job = jobPlyTask
	m.activeTaskIntent = strings.TrimSpace(m.composer.Value())
	m.composer.SetValue("")
	m.stdout.Reset()
}

type fakeRecorder struct {
	req askexec.RecordRequest
	err error
}

func (f *fakeRecorder) Record(_ context.Context, req askexec.RecordRequest) error {
	f.req = req
	return f.err
}

func (f *fakeRunner) Replay(_ context.Context, path string) <-chan askexec.Event {
	f.replayPath = path
	return f.replayEvents
}

func (f *fakeRunner) Start(_ context.Context, req askexec.Request) <-chan askexec.Event {
	f.req = req
	return f.events
}

func TestDefaultSubmitRunsReplayableTaskAndKeepsToolActivity(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event, 4)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", Model: "test/model", InitialPrompt: "build it"})
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || !m.running {
		t.Fatal("submit did not start a task")
	}
	if task.req.Goal != "build it" || task.req.Session != "/tmp/run.jsonl" || task.req.Dir != "/work" || task.req.Model != "test/model" {
		t.Fatalf("request = %#v", task.req)
	}

	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stderr, Text: "$ rg TODO\nfound one\n"})
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stdout, Text: "working result"})
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)

	if m.running || len(m.messages) != 3 || m.messages[1].role != roleTools {
		t.Fatalf("running=%v messages=%#v", m.running, m.messages)
	}
	if got := m.messages[2].text; got != "working result" {
		t.Fatalf("answer = %q", got)
	}
	if !strings.Contains(m.messages[1].text, "rg TODO") || !strings.Contains(m.notice, "no executable check") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestContractSubmitCreatesReviewDraftBeforeAnyPlyWork(t *testing.T) {
	contracts := &fakeNegotiator{compileEvents: make(chan contractexec.DraftEvent)}
	task := &fakeTask{events: make(chan plyexec.Event)}
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	m := New(Config{
		Task: task, Contracts: contracts, Session: sessionPath, DataDir: t.TempDir(), Workspace: "/work",
		InitialPrompt: "make an excellent gallery", TaskOptions: plyexec.TaskOptions{IntentContract: true},
	})
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || len(contracts.compileReqs) != 1 || task.calls != 0 || m.job != jobContractDraft {
		t.Fatalf("compile=%d ply=%d job=%d cmd=%v", len(contracts.compileReqs), task.calls, m.job, cmd != nil)
	}
	draft, err := m.contractStore.SaveDraft(contractexec.Draft{
		Schema: 1, OutcomeID: "outcome", Generation: 1, Intent: "make an excellent gallery", Workspace: "/work",
		Contract: []byte(testContractJSON), CompilerEvidenceSHA256: "sha256:evidence",
		CheckSHA256: "sha256:empty", Skills: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = m.Update(contractDraftProcessEvent{Done: true, ExitCode: 0, Draft: &draft})
	m = updated.(*Model)
	if m.screen != screenContract || m.contractDraft == nil || task.calls != 0 || !strings.Contains(m.notice, "review, revise, edit") {
		t.Fatalf("screen=%d draft=%v ply=%d notice=%q", m.screen, m.contractDraft != nil, task.calls, m.notice)
	}
	m.composer.SetValue("make mobile legibility explicit")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if len(contracts.compileReqs) != 2 || contracts.compileReqs[1].Current == nil || contracts.compileReqs[1].Instruction != "make mobile legibility explicit" || task.calls != 0 {
		t.Fatalf("revision=%#v ply=%d", contracts.compileReqs, task.calls)
	}
}

func TestMissingContractControllerFailsClosedBeforeTaskRunner(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, InitialPrompt: "build", TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if task.calls != 0 || m.running || !strings.Contains(m.notice, "Ply has not started") {
		t.Fatalf("calls=%d running=%v notice=%q", task.calls, m.running, m.notice)
	}
}

func TestContractOffAllowsDirectWorkWhileRetainingDraft(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.contractDraft = &contractexec.Draft{OutcomeID: "retained"}
	m.composer.SetValue("/contract off")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	m.composer.SetValue("run directly")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if task.calls != 1 || task.req.Options.IntentContract || m.contractDraft == nil {
		t.Fatalf("calls=%d options=%#v draft=%#v", task.calls, task.req.Options, m.contractDraft)
	}
}

func TestContractAdmissionRefusesUnsentRevisionText(t *testing.T) {
	contracts := &fakeNegotiator{plyEvents: make(chan plyexec.Event)}
	m := New(Config{Task: &fakeTask{}, Contracts: contracts, TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.screen = screenContract
	m.contractDraft = &contractexec.Draft{OutcomeID: "draft"}
	m.composer.SetValue("add mobile inspection")
	updated, _ := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if len(contracts.admitReqs) != 0 || !strings.Contains(m.notice, "press Enter to revise") || m.composer.Value() == "" {
		t.Fatalf("admissions=%d notice=%q text=%q", len(contracts.admitReqs), m.notice, m.composer.Value())
	}
}

func TestContractAdmissionRefusesDraftChangedSinceDisplay(t *testing.T) {
	contracts := &fakeNegotiator{plyEvents: make(chan plyexec.Event)}
	m := New(Config{
		Task: &fakeTask{}, Contracts: contracts, Session: filepath.Join(t.TempDir(), "session.jsonl"),
		DataDir: t.TempDir(), Workspace: "/work", TaskOptions: plyexec.TaskOptions{IntentContract: true},
	})
	shown, err := m.contractStore.SaveDraft(contractexec.Draft{
		Schema: 1, OutcomeID: "outcome", Generation: 1, Intent: "make gallery", Workspace: "/work",
		Contract: []byte(testContractJSON), CompilerEvidenceSHA256: "sha256:evidence", CheckSHA256: "sha256:empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	shown, err = m.contractStore.MarkDraftRecorded(shown)
	if err != nil {
		t.Fatal(err)
	}
	m.contractDraft = &shown
	newer := shown
	newer.Generation++
	newer.Contract = []byte(strings.Replace(testContractJSON, "A complete gallery exists.", "A reviewed mobile gallery exists.", 1))
	newer, err = m.contractStore.SaveDraftCAS(newer, shown.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	newer, err = m.contractStore.MarkDraftRecorded(newer)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.acceptContractDraft()
	m = updated.(*Model)
	if len(contracts.admitReqs) != 0 || m.contractDraft == nil || m.contractDraft.DraftSHA256 != newer.DraftSHA256 || !strings.Contains(m.notice, "changed since it was shown") {
		t.Fatalf("admissions=%d displayed=%#v notice=%q", len(contracts.admitReqs), m.contractDraft, m.notice)
	}
}

func TestContractAcceptanceIsDistinctAndStartsOnlyNegotiatorAdmission(t *testing.T) {
	contracts := &fakeNegotiator{plyEvents: make(chan plyexec.Event)}
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Contracts: contracts, Recorder: &fakeRecorder{}, Session: filepath.Join(t.TempDir(), "session.jsonl"),
		DataDir: t.TempDir(), Workspace: "/work", TaskOptions: plyexec.TaskOptions{IntentContract: true},
	})
	draft, err := m.contractStore.SaveDraft(contractexec.Draft{
		Schema: 1, OutcomeID: "outcome", Generation: 1, Intent: "make gallery", Workspace: "/work",
		Contract: []byte(testContractJSON), CompilerEvidenceSHA256: "sha256:evidence",
		CheckSHA256: "sha256:empty", Skills: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.contractDraft = &draft
	m.screen = screenContract
	m.composer.SetValue("/accept")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if len(contracts.admitReqs) != 0 || !strings.Contains(m.notice, "/contract accept") {
		t.Fatalf("admissions=%d notice=%q", len(contracts.admitReqs), m.notice)
	}
	m.composer.SetValue("/contract accept")
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || len(contracts.admitReqs) != 1 || task.calls != 0 || contracts.admitReqs[0].ExpectedDraftSHA256 != draft.DraftSHA256 {
		t.Fatalf("admissions=%#v task=%d cmd=%v", contracts.admitReqs, task.calls, cmd != nil)
	}
}

func TestContinueUsesSameAdmittedContractWithoutRecompiling(t *testing.T) {
	contracts := &fakeNegotiator{plyEvents: make(chan plyexec.Event)}
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Contracts: contracts, Session: filepath.Join(t.TempDir(), "session.jsonl"), DataDir: t.TempDir(), Workspace: "/work", TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	draft := contractexec.Draft{Schema: 1, OutcomeID: "outcome", RevisionID: "sha256:revision", ContractID: "sha256:admission", Intent: "make gallery", Workspace: "/work", Contract: []byte(testContractJSON), CheckSHA256: "sha256:empty"}
	m.admittedContract = &draft
	m.pendingContract = &plyexec.ContractResult{ContractID: draft.ContractID, Status: "review_required", Outstanding: []plyexec.ContractCriterion{{ID: "quality", Judge: "human"}}}
	m.composer.SetValue("/continue")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	m.composer.SetValue("strengthen the perspective and rerender")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if len(contracts.runReqs) != 1 || len(contracts.compileReqs) != 0 || contracts.runReqs[0].Draft.ContractID != draft.ContractID || contracts.runReqs[0].Guidance != "strengthen the perspective and rerender" {
		t.Fatalf("runs=%#v compiles=%#v", contracts.runReqs, contracts.compileReqs)
	}
}

func TestCanceledAdmissionReconcilesDurableAdmittedState(t *testing.T) {
	m := New(Config{
		Task: &fakeTask{}, Contracts: &fakeNegotiator{}, Session: filepath.Join(t.TempDir(), "session.jsonl"),
		DataDir: t.TempDir(), Workspace: "/work", TaskOptions: plyexec.TaskOptions{IntentContract: true},
	})
	draft, err := m.contractStore.SaveDraft(contractexec.Draft{
		Schema: 1, OutcomeID: "outcome", Generation: 1, Intent: "make gallery", Workspace: "/work",
		Contract: []byte(testContractJSON), CompilerEvidenceSHA256: "sha256:evidence", CheckSHA256: "sha256:empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = m.contractStore.MarkDraftRecorded(draft)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := m.contractStore.PublishRevision(draft, draft.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.contractStore.MarkAdmitted(admitted); err != nil {
		t.Fatal(err)
	}
	m.contractDraft = &draft
	m.running = true
	m.job = jobPlyTask
	updated, _ := m.updateTaskProcess(plyexec.Event{Done: true, ExitCode: 1, Err: context.Canceled})
	m = updated.(*Model)
	if m.contractDraft != nil || m.admittedContract == nil || m.admittedContract.ContractID != admitted.ContractID {
		t.Fatalf("draft=%#v admitted=%#v notice=%q", m.contractDraft, m.admittedContract, m.notice)
	}
}

const testContractJSON = `{"version":2,"outcome":"A complete gallery exists.","deliverables":["gallery.html"],"invariants":["source remains"],"criteria":[{"id":"quality","requirement":"gallery is legible","evidence":"direct inspection","judge":"human"}],"approvals":[],"assumptions":[],"open_questions":[],"limits":[]}`

func TestContractDraftPresentationSanitizesTerminalControls(t *testing.T) {
	body := strings.Replace(testContractJSON, "A complete gallery exists.", `A gallery \u001b[2J\u001b]8;;https://evil.invalid\u0007spoof\u001b]8;;\u0007 exists.`, 1)
	draft := contractexec.Draft{
		Generation: 1, OutcomeID: "outcome", DraftSHA256: "sha256:draft", Contract: []byte(body),
		Intent: "create a gallery", Workspace: "/work/gallery", Toolbox: "/tools/safe", Check: "test -s gallery.html",
		CheckAll: true, Skills: []string{"ascii-cinema"}, CompilerEvidenceSHA256: "sha256:evidence",
	}
	card := contractDraftCard(draft, nil, "/tmp/\x1b[31m/draft.json")
	if strings.ContainsRune(card, '\x1b') || strings.ContainsRune(card, '\x00') {
		t.Fatalf("contract card retained terminal controls: %q", card)
	}
	for _, want := range []string{"Workspace: /work/gallery", "Tools: toolbox /tools/safe", "Check: test -s gallery.html", "operator-admitted judge", "Brief skills: ascii-cinema"} {
		if !strings.Contains(card, want) {
			t.Errorf("semantic contract card omitted execution policy %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "sha256:evidence") {
		t.Fatalf("semantic contract card leaked audit digest:\n%s", card)
	}
	audit := contractBindings(draft)
	for _, want := range []string{"AUDIT DETAILS", "sha256:draft", "outcome", "not admitted", "sha256:evidence"} {
		if !strings.Contains(audit, want) {
			t.Errorf("contract audit omitted admission binding %q:\n%s", want, audit)
		}
	}
}

func TestContractReviewDefaultsToSemanticSummaryAndTogglesAudit(t *testing.T) {
	draft := contractexec.Draft{
		Generation: 2, OutcomeID: "outcome", DraftSHA256: "sha256:draft", Contract: []byte(testContractJSON),
		Intent: "create a gallery", Workspace: "/work/gallery", Toolbox: "/tools/safe", Check: "test -s gallery.html",
		Skills: []string{"ascii-cinema"}, CompilerEvidenceSHA256: "sha256:evidence",
	}
	m := New(Config{TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.screen = screenContract
	m.contractDraft = &draft
	semantic := m.renderContract(100)
	if !strings.Contains(semantic, "Outcome:") || !strings.Contains(semantic, "Deliverables:") || strings.Contains(semantic, "sha256:evidence") || !strings.Contains(semantic, "/tools/safe") {
		t.Fatalf("semantic review leaked or omitted fields:\n%s", semantic)
	}
	updated, _ := m.Update(key("a"))
	m = updated.(*Model)
	audit := m.renderContract(100)
	for _, want := range []string{"AUDIT DETAILS", "sha256:draft", "sha256:evidence", "/tools/safe", "ascii-cinema"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit review omitted %q:\n%s", want, audit)
		}
	}
}

func TestContractReviewAllowsLetterAInsideRevisionText(t *testing.T) {
	m := New(Config{TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.screen = screenContract
	m.composer.SetValue("Make ")
	m.composer.Focus()
	updated, _ := m.Update(key("a"))
	m = updated.(*Model)
	if m.contractAudit || m.composer.Value() != "Make a" {
		t.Fatalf("audit=%v composer=%q", m.contractAudit, m.composer.Value())
	}
}

func TestContractAuditCommandOpensExistingContractAndPreservesMissingNotice(t *testing.T) {
	draft := contractexec.Draft{Generation: 1, OutcomeID: "outcome", DraftSHA256: "sha256:draft", Contract: []byte(testContractJSON)}
	m := New(Config{DataDir: t.TempDir(), TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.contractDraft = &draft
	m.screen = screenAsk
	m.composer.SetValue("/contract audit")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if m.screen != screenContract || !m.contractAudit || !strings.Contains(m.notice, "audit details shown") {
		t.Fatalf("screen=%v audit=%v notice=%q", m.screen, m.contractAudit, m.notice)
	}

	missing := New(Config{DataDir: t.TempDir(), TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	missing.composer.SetValue("/contract audit")
	updated, _ = missing.Update(key("enter"))
	missing = updated.(*Model)
	if missing.screen == screenContract || missing.contractAudit || !strings.Contains(missing.notice, "No durable contract") {
		t.Fatalf("screen=%v audit=%v notice=%q", missing.screen, missing.contractAudit, missing.notice)
	}
}

func TestContractRevisionCardNamesSemanticChanges(t *testing.T) {
	before := contractexec.Draft{OutcomeID: "same-outcome", Contract: []byte(testContractJSON), Intent: "gallery", Workspace: "/work"}
	after := before
	after.Generation = 2
	after.Contract = []byte(strings.Replace(testContractJSON, "gallery is legible", "gallery is legible at mobile widths", 1))
	card := contractDraftCard(after, &before, "/tmp/draft.json")
	if !strings.Contains(card, "Changed: evidence") || strings.Contains(card, "sha256:") {
		t.Fatalf("revision card did not summarize semantic change:\n%s", card)
	}
}

func TestContractCardDoesNotDiffUnrelatedOutcomes(t *testing.T) {
	before := contractexec.Draft{OutcomeID: "old", Contract: []byte(testContractJSON)}
	after := contractexec.Draft{OutcomeID: "new", Generation: 1, Contract: []byte(strings.Replace(testContractJSON, "A complete gallery exists.", "A new report exists.", 1))}
	card := contractDraftCard(after, &before, "/tmp/draft.json")
	if !strings.Contains(card, "Initial proposal") || strings.Contains(card, "Changed:") {
		t.Fatalf("unrelated outcome was rendered as an amendment:\n%s", card)
	}
}

func TestTaskExitTwoRemainsNotDone(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "finish it",
		TaskOptions: plyexec.TaskOptions{Check: "go test ./..."},
	})
	updated, _ := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stderr, Text: "$ go test ./...\nFAIL\n"})
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Err: &fakeExitError{}})
	m = updated.(*Model)
	if m.running || !strings.Contains(m.notice, "not done") || strings.Contains(strings.ToLower(m.notice), "passed") {
		t.Fatalf("running=%v notice=%q", m.running, m.notice)
	}
	if len(m.messages) != 2 || m.messages[1].role != roleTools || !strings.Contains(m.messages[1].text, "FAIL") {
		t.Fatalf("failed task evidence=%#v", m.messages)
	}
	if m.taskOptions.Check != "go test ./..." {
		t.Fatalf("not-done outcome lost its check: %q", m.taskOptions.Check)
	}
}

func TestTaskPolicyPropagatesAndCheckedSuccessNamesTheVerdict(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	options := plyexec.TaskOptions{
		Check:  "go test ./...",
		Effort: "xhigh",
		Cycles: 0, HasCycles: true,
		Turns: 20, HasTurns: true,
		Timeout: time.Minute, HasTimeout: true,
		Compact:     true,
		Compactions: 2, HasCompactions: true,
	}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "finish it", TaskOptions: options})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if task.req.Options != options {
		t.Fatalf("task options=%#v, want %#v", task.req.Options, options)
	}
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0, Session: "/tmp/compacted.jsonl"})
	m = updated.(*Model)
	if m.session != "/tmp/compacted.jsonl" || !strings.Contains(m.notice, "check passed") || !strings.Contains(m.notice, "replayable") {
		t.Fatalf("session=%q notice=%q", m.session, m.notice)
	}
	if m.taskOptions.Check != "" || !strings.Contains(m.notice, "check cleared") {
		t.Fatalf("successful outcome retained check=%q notice=%q", m.taskOptions.Check, m.notice)
	}
}

func TestContractedMixedEvidenceIsReadyForReviewNotDone(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "make art",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "test -s gallery.html"},
	})
	armContractResultPresentation(m)
	result := &plyexec.ContractResult{
		Status: "review_required", CheckConfigured: true, CheckPassed: true, ProposedCheckCoverage: []string{"exists"},
		Outstanding: []plyexec.ContractCriterion{{ID: "layout", Judge: "inspection"}, {ID: "quality", Judge: "human"}},
	}
	updated, _ := m.Update(plyProcessEvent{
		Done: true, ExitCode: 2, Session: "/tmp/run.jsonl", ContractResult: result,
		Text: "Ready for review · configured check passed 1/3 criteria · 2 require inspection/human review · session is replayable\n",
	})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Ready for review") || strings.Contains(strings.ToLower(m.notice), "task done") || strings.Contains(strings.ToLower(m.notice), "outcome complete") {
		t.Fatalf("notice=%q", m.notice)
	}
	if m.taskOptions.Check == "" {
		t.Fatal("review-required outcome cleared its configured check")
	}
	transcript := m.renderTranscript(76)
	for _, want := range []string{"OUTCOME", "READY FOR REVIEW", "layout · inspection", "quality · human", "/accept", "/continue"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("review transcript missing %q:\n%s", want, transcript)
		}
	}
}

func TestContractedAllCheckProposalStillNeedsAcceptance(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "go test ./..."},
	})
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Session: "/tmp/run.jsonl", Text: "Ready for review · configured check passed · proposed coverage 2/2 criteria · 2 remain unaccepted · session is replayable\n", ContractResult: &plyexec.ContractResult{
		Status: "review_required", CheckConfigured: true, CheckPassed: true, ProposedCheckCoverage: []string{"tests", "artifact"}, Outstanding: []plyexec.ContractCriterion{{ID: "tests", Judge: "check"}, {ID: "artifact", Judge: "check"}},
	}})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Ready for review") || !strings.Contains(m.notice, "remain unaccepted") || m.taskOptions.Check == "" || strings.Contains(strings.ToLower(m.notice), "outcome complete") {
		t.Fatalf("notice=%q check=%q", m.notice, m.taskOptions.Check)
	}
}

func TestOperatorAdmittedCheckCompletionConsumesOneShotPolicy(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "go test ./...", CheckAllCriteria: true},
	})
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stdout, Text: "Built it."})
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0, Session: "/tmp/run.jsonl", Text: "Outcome complete · operator-admitted check passed 2/2 criteria · session is replayable\n", ContractResult: &plyexec.ContractResult{
		ContractID: "sha256:contract", Status: "complete", CheckConfigured: true, CheckPassed: true,
		AdmittedCheckCoverage: []string{"tests", "artifact"}, Outstanding: []plyexec.ContractCriterion{},
	}})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Outcome complete") || !strings.Contains(m.notice, "2/2") || m.taskOptions.Check != "" || m.taskOptions.CheckAllCriteria || m.pendingContract != nil {
		t.Fatalf("notice=%q options=%#v pending=%#v", m.notice, m.taskOptions, m.pendingContract)
	}
	if len(m.messages) < 2 || m.messages[len(m.messages)-2].text != "Built it." || m.messages[len(m.messages)-1].role != roleOutcome {
		t.Fatalf("messages=%#v", m.messages)
	}
	transcript := m.renderTranscript(76)
	for _, want := range []string{"OUTCOME", "COMPLETE", "Operator-admitted check settled 2", "next outcome"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("complete transcript missing %q:\n%s", want, transcript)
		}
	}
}

func TestEveryTerminalContractStateExplainsWhatHappenedAndWhatComesNext(t *testing.T) {
	tests := []struct {
		status string
		want   []string
	}{
		{status: "not_done", want: []string{"NOT DONE", "outcome check", "Next:"}},
		{status: "interrupted", want: []string{"INTERRUPTED", "replayable session", "Next:"}},
		{status: "failed", want: []string{"NOT ACCEPTED", "trustworthy outcome verdict", "Next:"}},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			card := contractResultCard(plyexec.ContractResult{Status: test.status})
			for _, want := range test.want {
				if !strings.Contains(card, want) {
					t.Errorf("card missing %q:\n%s", want, card)
				}
			}
		})
	}
}

func TestRunningTaskShowsUnderstandingThenLiveWorkspaceEvidence(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "fix it"})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stderr, Text: "compiling outcome contract\n"})
	m = updated.(*Model)
	before := m.renderTranscript(76)
	if !strings.Contains(before, "WORKING · LIVE") || !strings.Contains(before, "Understanding the outcome") || !strings.Contains(before, "compiling outcome contract") {
		t.Fatalf("understanding phase is opaque:\n%s", before)
	}

	updated, _ = m.Update(plyProcessEvent{Contract: "OUTCOME CONTRACT v2\nFix the parser.", ContractDigest: "abcdef"})
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stderr, Text: "$ rg parser\nsrc/parser.go:42\n"})
	m = updated.(*Model)
	after := m.renderTranscript(76)
	for _, want := range []string{"CONTRACT", "Using workspace tools", "$ rg parser", "src/parser.go:42"} {
		if !strings.Contains(after, want) {
			t.Errorf("live work phase missing %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, "compiling outcome contract") {
		t.Fatalf("compiler progress leaked into workspace work log:\n%s", after)
	}
}

func TestLiveWorkKeepsRecentEvidenceWithoutPretendingEarlierOutputVanished(t *testing.T) {
	got := tailLines("one\ntwo\nthree\nfour", 2)
	if !strings.Contains(got, "earlier work remains in the replayable session") || !strings.Contains(got, "three\nfour") || strings.Contains(got, "one") {
		t.Fatalf("tail=%q", got)
	}
}

func TestAcceptSealsInteractiveDecisionAndClearsCheck(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	recorder := &fakeRecorder{}
	m := New(Config{
		Task: task, Recorder: recorder, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "go test ./..."},
	})
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Session: "/tmp/run.jsonl", ContractResult: &plyexec.ContractResult{
		ContractID: "sha256:contract", Status: "review_required", CheckConfigured: true, CheckPassed: true,
		ProposedCheckCoverage: []string{"tests"}, Outstanding: []plyexec.ContractCriterion{{ID: "tests", Judge: "check"}, {ID: "quality", Judge: "human"}},
	}})
	m = updated.(*Model)
	m.composer.SetValue("/accept")
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || !m.running {
		t.Fatal("accept did not start a record operation")
	}
	updated, _ = m.Update(cmd().(contractAcceptanceMsg))
	m = updated.(*Model)
	if recorder.req.Session != "/tmp/run.jsonl" || recorder.req.Kind != "bench.contract-acceptance/v1" || recorder.req.Source != "bench-user" || !strings.Contains(recorder.req.JSON, `"result_sha256":"sha256:`) || !strings.Contains(recorder.req.JSON, `"criteria":["tests","quality"]`) {
		t.Fatalf("record=%#v", recorder.req)
	}
	if m.pendingContract != nil || m.taskOptions.Check != "" || !strings.Contains(m.notice, "Outcome accepted") {
		t.Fatalf("pending=%#v check=%q notice=%q", m.pendingContract, m.taskOptions.Check, m.notice)
	}
	if transcript := m.renderTranscript(76); !strings.Contains(transcript, "ACCEPTED BY YOU") || !strings.Contains(transcript, "sealed in the replayable session") {
		t.Fatalf("acceptance is not durable in transcript:\n%s", transcript)
	}
}

func TestReviewBlocksOrdinaryWorkUntilExplicitContinue(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	contracts := &fakeNegotiator{plyEvents: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Contracts: contracts, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	})
	m.admittedContract = &contractexec.Draft{ContractID: "sha256:contract", RevisionID: "sha256:revision", Intent: "build", Workspace: "/work", Check: "true", CheckSHA256: "sha256:check", Contract: []byte(testContractJSON)}
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, ContractResult: &plyexec.ContractResult{
		ContractID: "sha256:contract", Status: "review_required", Outstanding: []plyexec.ContractCriterion{{ID: "quality", Judge: "human"}},
	}})
	m = updated.(*Model)
	m.composer.SetValue("fix the mobile layout")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if task.calls != 0 || len(contracts.runReqs) != 0 || !strings.Contains(m.notice, "Review pending") {
		t.Fatalf("calls=%d runs=%d notice=%q", task.calls, len(contracts.runReqs), m.notice)
	}
	m.composer.SetValue("/continue")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.pendingContract != nil || !m.taskOptions.Force {
		t.Fatalf("pending=%#v force=%v", m.pendingContract, m.taskOptions.Force)
	}
	m.composer.SetValue("fix the mobile layout")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if len(contracts.runReqs) != 1 || !contracts.runReqs[0].Task.Options.Force || contracts.runReqs[0].Guidance != "fix the mobile layout" || m.taskOptions.Force {
		t.Fatalf("runs=%#v retained force=%v", contracts.runReqs, m.taskOptions.Force)
	}
}

func TestContractedTerminalWithoutResultFailsClosedInUI(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	})
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0, Session: "/tmp/run.jsonl"})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "without a sealed contract result") || strings.Contains(strings.ToLower(m.notice), "task done") || m.taskOptions.Check == "" {
		t.Fatalf("notice=%q check=%q", m.notice, m.taskOptions.Check)
	}
}

func TestContractOpenQuestionPausesBeforeWork(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "print it", TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Session: "/tmp/run.jsonl", Text: "Needs decision · 1 open question(s) and 1 approval(s) must be resolved before work begins\n", ContractResult: &plyexec.ContractResult{
		Status: "needs_decision", OpenQuestions: []string{"Which printer?"}, PendingApprovals: []string{"Send an external print job"}, Outstanding: []plyexec.ContractCriterion{{ID: "printed", Judge: "human"}},
	}})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Needs decision") || !strings.Contains(m.notice, "before work begins") || strings.Contains(strings.ToLower(m.notice), "done") {
		t.Fatalf("notice=%q", m.notice)
	}
	decision := m.renderTranscript(76)
	for _, want := range []string{"DECISION NEEDED", "paused before workspace tools ran", "Which printer?", "Approval required: Send an external print job", "reply normally"} {
		if !strings.Contains(decision, want) {
			t.Errorf("decision transcript missing %q:\n%s", want, decision)
		}
	}
	if hint := m.defaultWorkHint(); !strings.Contains(hint, "decision pending") || !strings.Contains(hint, "reply normally") {
		t.Fatalf("decision footer contradicts the outcome card: %q", hint)
	}
	if task.calls != 0 {
		t.Fatalf("legacy decision presentation unexpectedly ran Ply: %d", task.calls)
	}
}

func TestPassingPrecheckDoesNotClaimAReplayableModelTurn(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/not-created.jsonl", Workspace: "/work", InitialPrompt: "already done",
		TaskOptions: plyexec.TaskOptions{Check: "test -f result"},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "already passed") || !strings.Contains(m.notice, "no model turn") || strings.Contains(m.notice, "replayable") {
		t.Fatalf("notice=%q", m.notice)
	}
	if m.taskOptions.Check != "" {
		t.Fatalf("passing precheck retained check=%q", m.taskOptions.Check)
	}
}

func TestCompactedTaskSessionOwnsLaterAskOnlyTurn(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	runner := &fakeRunner{events: make(chan askexec.Event)}
	m := New(Config{
		Task: task, Runner: runner, Session: "/tmp/source.jsonl", Workspace: "/work", InitialPrompt: "work",
		TaskOptions: plyexec.TaskOptions{Compact: true},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Session: "/tmp/successor.jsonl"})
	m = updated.(*Model)
	if m.session != "/tmp/successor.jsonl" || !strings.Contains(m.notice, "not done") {
		t.Fatalf("session=%q notice=%q", m.session, m.notice)
	}
	m.composer.SetValue("/ask")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	m.composer.SetValue("continue")
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || runner.req.Session != "/tmp/successor.jsonl" {
		t.Fatalf("later Ask session=%q", runner.req.Session)
	}
}

func TestCompactionKeepsOneSubagentEvidenceLineage(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/source.jsonl", DataDir: "/work/.bench",
		Workspace: "/work", InitialPrompt: "first delegated turn",
		TaskOptions: plyexec.TaskOptions{Compact: true},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	first := task.req.SubagentsDir
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Session: "/tmp/successor.jsonl"})
	m = updated.(*Model)
	m.composer.SetValue("second delegated turn")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if first == "" || task.req.SubagentsDir != first {
		t.Fatalf("subagent lineage moved after compaction: first=%q second=%q", first, task.req.SubagentsDir)
	}
}

func TestSelectingAnotherSessionMovesTheSubagentEvidenceLineage(t *testing.T) {
	runner := &fakeRunner{replayEvents: make(chan askexec.Event)}
	m := New(Config{Runner: runner, Session: "/tmp/current.jsonl", NewSession: "/tmp/new.jsonl", DataDir: "/work/.bench"})
	initial := m.subagentsPath()
	updated, _ := m.startReplay("/tmp/saved.jsonl")
	m = updated.(*Model)
	if m.subagentsPath() == initial || !strings.Contains(m.subagentsPath(), "saved-") {
		t.Fatalf("selected session evidence path=%q initial=%q", m.subagentsPath(), initial)
	}
	selected := m.subagentsPath()
	updated, _ = m.startNew()
	m = updated.(*Model)
	if m.subagentsPath() == selected || !strings.Contains(m.subagentsPath(), "new-") {
		t.Fatalf("new session evidence path=%q selected=%q", m.subagentsPath(), selected)
	}
}

func TestSlashAskSwitchesToAskOnly(t *testing.T) {
	runner := &fakeRunner{events: make(chan askexec.Event)}
	m := New(Config{Runner: runner, Session: "/tmp/run.jsonl"})
	m.composer.SetValue("/ask")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if m.taskMode || !strings.Contains(m.notice, "Ask mode") {
		t.Fatalf("taskMode=%v notice=%q", m.taskMode, m.notice)
	}
	if got := m.View(); got.WindowTitle != "bench · ask" || !strings.Contains(got.Content, "ASK · NO MODEL-RUN TOOLS") {
		t.Fatalf("Ask-only grant is not visible: title=%q\n%s", got.WindowTitle, got.Content)
	}
	m.composer.SetValue("explain it")
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || m.job != jobTurn || runner.req.Message != "explain it" {
		t.Fatalf("job=%v request=%#v", m.job, runner.req)
	}
}

func TestSlashModelSwitchesFutureAskAndPlyTurns(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work"})
	m.composer.SetValue("/model openai-codex/gpt-test")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if m.modelName != "openai-codex/gpt-test" || !strings.Contains(m.notice, "Model switched") {
		t.Fatalf("model=%q notice=%q", m.modelName, m.notice)
	}
	m.composer.SetValue("inspect it")
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || task.req.Model != "openai-codex/gpt-test" {
		t.Fatalf("task request=%#v", task.req)
	}
}

func TestSlashToolsSelectsToolboxAndAskMode(t *testing.T) {
	workspace := t.TempDir()
	toolbox := filepath.Join(workspace, "tools")
	if err := os.Mkdir(toolbox, 0o700); err != nil {
		t.Fatal(err)
	}
	m := New(Config{Workspace: workspace})
	m.composer.SetValue("/tools tools")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if !m.taskMode || m.toolbox != toolbox || !strings.Contains(m.notice, "toolbox tools") {
		t.Fatalf("taskMode=%v toolbox=%q notice=%q", m.taskMode, m.toolbox, m.notice)
	}
	m.composer.SetValue("/tools off")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.taskMode {
		t.Fatal("/tools off left Ask + Ply enabled")
	}
}

func TestShellLikeComposerKeysAndSlashEscape(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work"})
	m.composer.SetValue("first")
	updated, cmd := m.Update(key("alt+enter"))
	m = updated.(*Model)
	if cmd != nil || m.composer.Value() != "first\n" || m.running {
		t.Fatalf("newline value=%q running=%v", m.composer.Value(), m.running)
	}
	m.composer.SetValue("//literal")
	updated, cmd = m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || task.req.Goal != "/literal" {
		t.Fatalf("literal slash request=%#v", task.req)
	}

	idle := New(Config{})
	_, quit := idle.Update(key("ctrl+d"))
	if quit == nil {
		t.Fatal("ctrl+d on an empty prompt did not request EOF/quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+d message=%T", quit())
	}

	_, suspend := idle.Update(key("ctrl+z"))
	if suspend == nil {
		t.Fatal("ctrl+z did not request job-control suspension")
	}
	if _, ok := suspend().(tea.SuspendMsg); !ok {
		t.Fatalf("ctrl+z message=%T", suspend())
	}
}

func TestSlashCommandsAreDiscoverableAndNeverAccidentalModelCalls(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", ActiveSkills: []string{"go-review"}})
	m.composer.SetValue("/status")
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd != nil || m.running || !strings.Contains(m.notice, "Ask + Ply") || !strings.Contains(m.notice, "1 skill") {
		t.Fatalf("status running=%v notice=%q", m.running, m.notice)
	}
	m.composer.SetValue("/unknown")
	updated, cmd = m.Update(key("enter"))
	m = updated.(*Model)
	if cmd != nil || m.running || !strings.Contains(m.notice, "Unknown command") {
		t.Fatalf("unknown command ran: running=%v notice=%q", m.running, m.notice)
	}
	m.composer.SetValue("/help")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	help := m.renderHelp(m.viewport.Width())
	if !m.showHelp || !strings.Contains(help, "OUTCOME & EVIDENCE") || !strings.Contains(help, "PARTNER & CAPABILITIES") || !strings.Contains(help, "/model SPEC") || !strings.Contains(help, "/check -- CMD") || !strings.Contains(help, "/check all") || !strings.Contains(help, "/accept") || !strings.Contains(help, "/continue") || !strings.Contains(help, "/shell") {
		t.Fatalf("help=%q", help)
	}
}

func TestCheckCommandPreservesLiteralVerifierAndCanClearIt(t *testing.T) {
	m := New(Config{TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	check := `printf '%s  %s\n' "one" "two" | grep -q 'one  two'`
	m.composer.SetValue("/check -- " + check)
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd != nil || m.running || m.taskOptions.Check != check {
		t.Fatalf("check=%q running=%v", m.taskOptions.Check, m.running)
	}
	if !strings.Contains(m.notice, strconv.Quote(check)) {
		t.Fatalf("set notice=%q", m.notice)
	}

	m.composer.SetValue("/check")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if !strings.Contains(m.notice, strconv.Quote(check)) {
		t.Fatalf("show notice=%q", m.notice)
	}

	m.composer.SetValue("/check all")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if !m.taskOptions.CheckAllCriteria || !strings.Contains(m.notice, "blanket admission") {
		t.Fatalf("check-all=%v notice=%q", m.taskOptions.CheckAllCriteria, m.notice)
	}
	m.width, m.height = 140, 24
	m.resize()
	if view := m.View().Content; !strings.Contains(view, "CHECK ALL "+strconv.Quote(check)) {
		t.Fatalf("composer omitted check-all policy: %q", view)
	}

	m.composer.SetValue("/check -- true")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.taskOptions.CheckAllCriteria {
		t.Fatal("changing the check retained blanket admission")
	}

	m.composer.SetValue("/check off")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.taskOptions.Check != "" || m.taskOptions.CheckAllCriteria || !strings.Contains(m.notice, "next work outcome will be unchecked") {
		t.Fatalf("check=%q check-all=%v notice=%q", m.taskOptions.Check, m.taskOptions.CheckAllCriteria, m.notice)
	}
}

func TestCheckAllNeedsContractAndConfiguredCheck(t *testing.T) {
	m := New(Config{})
	m.composer.SetValue("/check all")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Set a check first") || m.taskOptions.CheckAllCriteria {
		t.Fatalf("notice=%q options=%#v", m.notice, m.taskOptions)
	}
	m.taskOptions.Check = "true"
	m.composer.SetValue("/check all")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if !strings.Contains(m.notice, "contracts on") || m.taskOptions.CheckAllCriteria {
		t.Fatalf("notice=%q options=%#v", m.notice, m.taskOptions)
	}
}

func TestStatusShowsTheExactConfiguredWorkCheck(t *testing.T) {
	check := `go test ./... && printf "literal spaces"`
	m := New(Config{TaskOptions: plyexec.TaskOptions{Check: check}})
	m.taskMode = false
	m.composer.SetValue("/status")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Ask · no model-run tools") || !strings.Contains(m.notice, "work check "+strconv.Quote(check)) {
		t.Fatalf("status=%q", m.notice)
	}
	if !strings.Contains(m.notice, "subagents ") || !strings.Contains(m.notice, filepath.Join("subagents", "session-")) {
		// An empty session still has a stable hashed evidence directory.
		t.Fatalf("status omitted subagent evidence path: %q", m.notice)
	}
	transcript := m.renderTranscript(80)
	for _, want := range []string{"STATUS", "Mode: Ask", "Tools: No model-run tools", "Outcome contract: Off", "Check: " + strconv.Quote(check), "Session evidence:", "Subagent evidence:"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("status transcript missing %q:\n%s", want, transcript)
		}
	}
	help := m.renderHelp(80)
	if !strings.Contains(help, strconv.Quote(check)) || !strings.Contains(help, "up to 3 read-heavy jobs; root synthesizes") {
		t.Fatalf("help omitted configured check: %q", help)
	}
	m.width, m.height = 140, 24
	m.taskMode = true
	m.resize()
	if view := m.View().Content; !strings.Contains(view, "CHECK "+strconv.Quote(check)) {
		t.Fatalf("composer omitted configured check: %q", view)
	}
}

func TestContractCommandControlsOnlyFutureOutcomeCompilation(t *testing.T) {
	m := New(Config{TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.composer.SetValue("/contract")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if !strings.Contains(m.notice, "No durable contract") {
		t.Fatalf("notice=%q", m.notice)
	}
	m.composer.SetValue("/contract off")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.taskOptions.IntentContract || !strings.Contains(m.notice, "starts Ply") {
		t.Fatalf("contract=%v notice=%q", m.taskOptions.IntentContract, m.notice)
	}
	m.composer.SetValue("/contract on")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if !m.taskOptions.IntentContract || !strings.Contains(m.taskPolicyDisplay(), "autonomy review") {
		t.Fatalf("contract=%v policy=%q", m.taskOptions.IntentContract, m.taskPolicyDisplay())
	}
}

func TestAutonomyModeMakesQuickAndReviewExplicit(t *testing.T) {
	m := New(Config{TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	m.pendingContract = &plyexec.ContractResult{}
	m.taskOptions.Force = true
	m.continueContract = true
	m.composer.SetValue("/mode quick")
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	if m.autonomyMode() != autonomy.Quick || m.taskOptions.IntentContract || m.pendingContract != nil || m.taskOptions.Force || m.continueContract || !strings.Contains(m.notice, "start Ply") {
		t.Fatalf("mode=%q contract=%v notice=%q", m.autonomyMode(), m.taskOptions.IntentContract, m.notice)
	}
	m.composer.SetValue("/mode review")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.autonomyMode() != autonomy.Review || !m.taskOptions.IntentContract || !strings.Contains(m.notice, "durable outcome") {
		t.Fatalf("mode=%q contract=%v notice=%q", m.autonomyMode(), m.taskOptions.IntentContract, m.notice)
	}
	m.composer.SetValue("/mode loop")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.autonomyMode() != autonomy.Review || !strings.Contains(m.notice, "not supported") {
		t.Fatalf("mode=%q notice=%q", m.autonomyMode(), m.notice)
	}
}

func TestTaskShowsCompiledContractBeforeWorkEvidence(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event, 3)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build it"})
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("task did not start")
	}
	task.events <- plyexec.Event{Contract: "OUTCOME CONTRACT v2 · abcdef\nBuild the artifact.", ContractDigest: "abcdef"}
	updated, _ = m.Update(plyProcessEvent(<-task.events))
	m = updated.(*Model)
	if len(m.messages) < 2 || m.messages[1].role != roleContract || !strings.Contains(m.messages[1].text, "Build the artifact") {
		t.Fatalf("messages=%#v", m.messages)
	}
	if !strings.Contains(m.notice, "abcdef") {
		t.Fatalf("notice=%q", m.notice)
	}
}

func TestSubmitPassesOnlyExplicitlyActiveBriefSkills(t *testing.T) {
	runner := &fakeRunner{events: make(chan askexec.Event)}
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Runner: runner, Session: "/tmp/run.jsonl", InitialPrompt: "review it", ActiveSkills: []string{"go-review", "house-style"}})
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || !m.running {
		t.Fatal("skilled turn did not start")
	}
	if got := strings.Join(task.req.Skills, ","); got != "go-review,house-style" {
		t.Fatalf("skills = %q", got)
	}
}

func TestExitTwoRemainsContextFull(t *testing.T) {
	m := New(Config{Runner: &fakeRunner{events: make(chan askexec.Event)}, Session: "/tmp/run.jsonl"})
	m.running = true
	updated, _ := m.Update(processEvent{Done: true, ExitCode: 2, Err: &fakeExitError{}})
	m = updated.(*Model)
	if !strings.Contains(strings.ToLower(m.notice), "context full") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestResizeKeepsComposerAndTranscriptUsable(t *testing.T) {
	m := New(Config{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	if m.viewport.Width() < 20 || m.viewport.Height() < 3 {
		t.Fatalf("viewport is unusable: %dx%d", m.viewport.Width(), m.viewport.Height())
	}
	view := m.View()
	if !view.AltScreen || view.WindowTitle != "bench · ask+ply" || !strings.Contains(view.Content, "ASK + PLY · FULL SHELL") || !strings.Contains(view.Content, "Ask default · ● ready") {
		t.Fatalf("view missing terminal contract: %#v", view)
	}
}

func TestDefaultTaskScreensFitEightyByTwentyFour(t *testing.T) {
	m := New(Config{Workspace: "/work", Session: "/work/.bench/sessions/task.jsonl", Model: "test/model", TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if got := m.View().Content; !strings.Contains(got, "What outcome should we pursue?") || !strings.Contains(got, "NEGOTIATE") || !strings.Contains(got, "ADMIT") || !strings.Contains(got, "VERIFY") || !strings.Contains(got, "/check -- COMMAND") || !strings.Contains(got, "FULL SHELL") {
		t.Fatalf("default task contract is not visible:\n%s", got)
	}

	m.messages = []message{
		{role: roleUser, text: "Find the failing check and fix the smallest root cause."},
		{role: roleTools, text: strings.Repeat("$ rg failure\ninternal/check.go:42: failure\n", 24)},
		{role: roleAssistant, text: "The parser accepted an empty verdict; I tightened it and the focused test now passes."},
	}
	m.notice = "Task stopped · replayable session · no executable check"
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if got := m.View().Content; !strings.Contains(got, "WORK LOG · END") || !strings.Contains(got, "no executable check") {
		t.Fatalf("completed task evidence is not visible:\n%s", got)
	}

	m.showHelp = true
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
}

func TestPartnerJourneyKeepsVerdictAndNextActionVisibleAtEightyByTwentyFour(t *testing.T) {
	m := New(Config{Workspace: "/work", Session: "/work/.bench/sessions/task.jsonl", Model: "test/model"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.messages = []message{
		{role: roleUser, text: "Make the gallery readable on mobile."},
		{role: roleContract, text: "OUTCOME CONTRACT v2\nA readable gallery exists."},
		{role: roleTools, text: strings.Repeat("$ inspect gallery\nviewport evidence\n", 20)},
		{role: roleAssistant, text: "I updated the layout and gathered viewport evidence."},
		{role: roleOutcome, text: "READY FOR REVIEW\nStill needs acceptance:\n- mobile-layout · inspection\nNext: inspect the artifacts, then /accept · or /continue to revise."},
	}
	m.notice = "Ready for review · 1 criterion remains"
	m.syncContent()
	view := m.View().Content
	assertTerminalBounds(t, view, 80, 24)
	for _, want := range []string{"OUTCOME", "READY FOR REVIEW", "mobile-layout · inspection", "/accept", "/continue", "1 criterion remains"} {
		if !strings.Contains(view, want) {
			t.Errorf("80x24 outcome view missing %q:\n%s", want, view)
		}
	}
}

func TestLongWorkspaceAndModelStillFitEightyColumns(t *testing.T) {
	m := New(Config{
		Workspace: "/work/a-workspace-name-that-is-deliberately-much-longer-than-the-header-can-display-without-truncation",
		Session:   "/work/.bench/sessions/task.jsonl",
		Model:     "a-provider-with-a-long-name/a-model-with-an-even-longer-name",
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if got := m.View().Content; !strings.Contains(got, "● ready") || !strings.Contains(got, "ASK + PLY · FULL SHELL") {
		t.Fatalf("header lost useful state while truncating:\n%s", got)
	}
}

func TestExplicitToolboxIsVisibleInsteadOfFullShell(t *testing.T) {
	m := New(Config{Workspace: "/work", Toolbox: "/work/.bench/tools"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	got := m.View().Content
	if !strings.Contains(got, "ASK + PLY · TOOLBOX tools") || strings.Contains(got, "ASK + PLY · FULL SHELL") {
		t.Fatalf("toolbox grant is ambiguous:\n%s", got)
	}
}

func TestBubbleTeaModelIsPointerOwned(t *testing.T) {
	m := New(Config{})
	if _, ok := any(m).(tea.Model); !ok {
		t.Fatal("*Model no longer implements tea.Model")
	}
	if _, ok := any(*m).(tea.Model); ok {
		t.Fatal("Model value must not be copied into Bubble Tea while cursor commands are live")
	}
}

func TestSafeTextDropsTerminalControlSequences(t *testing.T) {
	got := safeText("ok\x1b[2J\x00\rnext")
	if got != "ok\nnext" {
		t.Fatalf("safe text = %q", got)
	}
}

func TestContractPresentationDropsTerminalControlSequences(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "work", TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	armContractResultPresentation(m)
	var updated tea.Model
	updated, _ = m.Update(plyProcessEvent{Contract: "OUTCOME\x1b[2J\x1b]0;spoof\a\x00\nvisible", ContractDigest: "abc"})
	m = updated.(*Model)
	if len(m.messages) != 1 || m.messages[0].role != roleContract || m.messages[0].text != "OUTCOME\nvisible" {
		t.Fatalf("messages=%#v", m.messages)
	}
}

func TestPickerMakesResumeExplicitAndRestoresPublicReplay(t *testing.T) {
	runner := &fakeRunner{replayEvents: make(chan askexec.Event, 3)}
	m := New(Config{
		Runner:     runner,
		Session:    "/tmp/new.jsonl",
		NewSession: "/tmp/new.jsonl",
		Choose:     true,
		Sessions: []session.Info{
			{Path: "/tmp/saved.jsonl", Name: "saved", Size: 2048},
		},
	})
	if !m.picking {
		t.Fatal("saved sessions did not open the explicit picker")
	}
	updated, _ := m.Update(key("down"))
	m = updated.(*Model)
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || !m.running || m.job != jobReplay {
		t.Fatalf("resume did not start: running=%v job=%v", m.running, m.job)
	}
	if runner.replayPath != "/tmp/saved.jsonl" {
		t.Fatalf("replay path = %q", runner.replayPath)
	}

	updated, _ = m.Update(processEvent{Stream: askexec.Stdout, Text: "session saved\n» old requirement\nold answer\n"})
	m = updated.(*Model)
	updated, _ = m.Update(processEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.picking || m.running || !strings.Contains(m.restored, "old requirement") {
		t.Fatalf("restore state: picking=%v running=%v restored=%q", m.picking, m.running, m.restored)
	}
	if !strings.Contains(m.notice, "verified") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestPickerCanStartNewWithoutTouchingSavedSession(t *testing.T) {
	m := New(Config{
		Runner:     &fakeRunner{},
		Session:    "/tmp/new.jsonl",
		NewSession: "/tmp/new.jsonl",
		Choose:     true,
		Sessions:   []session.Info{{Path: "/tmp/saved.jsonl", Name: "saved"}},
	})
	updated, _ := m.Update(key("n"))
	m = updated.(*Model)
	if m.picking || m.session != "/tmp/new.jsonl" {
		t.Fatalf("new state: picking=%v session=%q", m.picking, m.session)
	}
}

func TestFailedVerificationReturnsToPicker(t *testing.T) {
	runner := &fakeRunner{replayEvents: make(chan askexec.Event)}
	m := New(Config{
		Runner:     runner,
		Session:    "/tmp/bad.jsonl",
		NewSession: "/tmp/new.jsonl",
		Resume:     true,
		Sessions:   []session.Info{{Path: "/tmp/bad.jsonl", Name: "bad"}},
	})
	updated, _ := m.Update(beginReplayMsg{})
	m = updated.(*Model)
	updated, _ = m.Update(processEvent{Stream: askexec.Stderr, Text: "replay divergence"})
	m = updated.(*Model)
	updated, _ = m.Update(processEvent{Done: true, ExitCode: 1, Err: &fakeExitError{}})
	m = updated.(*Model)
	if !m.picking || m.restored != "" {
		t.Fatalf("unverified session was admitted: picking=%v restored=%q", m.picking, m.restored)
	}
}

func TestReplayProgressDoesNotShowTheEmptyState(t *testing.T) {
	m := New(Config{})
	m.running = true
	m.job = jobReplay
	m.activity = "verifying session"
	m.syncContent()
	content := m.viewport.View()
	if !strings.Contains(content, "SESSION") || strings.Contains(content, "What are we working on") {
		t.Fatalf("replay progress = %q", content)
	}
}

func TestPickerKeepsSelectedSessionVisible(t *testing.T) {
	var saved []session.Info
	for i := 0; i < 20; i++ {
		saved = append(saved, session.Info{Path: "/tmp/session.jsonl", Name: strings.Repeat("x", i+1)})
	}
	m := New(Config{Choose: true, Sessions: saved})
	m.viewport.SetHeight(6)
	for i := 0; i < 14; i++ {
		updated, _ := m.Update(key("down"))
		m = updated.(*Model)
	}
	if m.selected != 14 || m.viewport.YOffset() == 0 {
		t.Fatalf("selected=%d offset=%d", m.selected, m.viewport.YOffset())
	}
}

type fakeExitError struct{}

func (*fakeExitError) Error() string { return "exit status 2" }

func key(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s}
}
