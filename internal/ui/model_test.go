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

func (f *fakeTask) Work(_ context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	f.req = req
	f.requests = append(f.requests, req)
	f.calls++
	return f.events
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
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	result := &plyexec.ContractResult{
		Status: "review_required", CheckConfigured: true, CheckPassed: true, ProposedCheckCoverage: []string{"exists"},
		Outstanding: []plyexec.ContractCriterion{{ID: "layout", Judge: "inspection"}, {ID: "quality", Judge: "human"}},
	}
	updated, _ = m.Update(plyProcessEvent{
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
}

func TestContractedAllCheckProposalStillNeedsAcceptance(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "go test ./..."},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
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
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
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
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].text != "Built it." {
		t.Fatalf("messages=%#v", m.messages)
	}
}

func TestAcceptSealsInteractiveDecisionAndClearsCheck(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	recorder := &fakeRecorder{}
	m := New(Config{
		Task: task, Recorder: recorder, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "go test ./..."},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
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
}

func TestReviewBlocksOrdinaryWorkUntilExplicitContinue(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, ContractResult: &plyexec.ContractResult{
		ContractID: "sha256:contract", Status: "review_required", Outstanding: []plyexec.ContractCriterion{{ID: "quality", Judge: "human"}},
	}})
	m = updated.(*Model)
	m.composer.SetValue("fix the mobile layout")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if task.calls != 1 || !strings.Contains(m.notice, "Review pending") {
		t.Fatalf("calls=%d notice=%q", task.calls, m.notice)
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
	if task.calls != 2 || !task.req.Options.Force || !strings.Contains(task.req.Goal, "fix the mobile layout") || m.taskOptions.Force {
		t.Fatalf("calls=%d req=%#v retained force=%v", task.calls, task.req, m.taskOptions.Force)
	}
}

func TestContractedTerminalWithoutResultFailsClosedInUI(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{
		Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "build",
		TaskOptions: plyexec.TaskOptions{IntentContract: true, Check: "true"},
	})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0, Session: "/tmp/run.jsonl"})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "without a sealed contract result") || strings.Contains(strings.ToLower(m.notice), "task done") || m.taskOptions.Check == "" {
		t.Fatalf("notice=%q check=%q", m.notice, m.taskOptions.Check)
	}
}

func TestContractOpenQuestionPausesBeforeWork(t *testing.T) {
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "print it", TaskOptions: plyexec.TaskOptions{IntentContract: true}})
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, Session: "/tmp/run.jsonl", Text: "Needs decision · 1 open question(s) and 1 approval(s) must be resolved before work begins\n", ContractResult: &plyexec.ContractResult{
		Status: "needs_decision", OpenQuestions: []string{"Which printer?"}, PendingApprovals: []string{"Send an external print job"}, Outstanding: []plyexec.ContractCriterion{{ID: "printed", Judge: "human"}},
	}})
	m = updated.(*Model)
	if !strings.Contains(m.notice, "Needs decision") || !strings.Contains(m.notice, "before work begins") || strings.Contains(strings.ToLower(m.notice), "done") {
		t.Fatalf("notice=%q", m.notice)
	}
	m.composer.SetValue("Office printer")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if task.calls != 2 {
		t.Fatalf("task calls=%d", task.calls)
	}
	resolved := task.requests[1].Goal
	for _, want := range []string{"print it", "Which printer?", "Send an external print job", "Do you approve?", "Office printer", "does not replace the original intent"} {
		if !strings.Contains(resolved, want) {
			t.Errorf("resolved intent missing %q:\n%s", want, resolved)
		}
	}
	if resolved == "Office printer" {
		t.Fatal("decision replaced the original intent")
	}
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 2, ContractResult: &plyexec.ContractResult{
		ContractID: "sha256:resolved", Status: "review_required", Outstanding: []plyexec.ContractCriterion{{ID: "printed", Judge: "human"}},
	}})
	m = updated.(*Model)
	if m.pendingDecision != nil || m.pendingContract == nil {
		t.Fatalf("decision=%#v review=%#v", m.pendingDecision, m.pendingContract)
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
	if !m.showHelp || !strings.Contains(help, "/model SPEC") || !strings.Contains(help, "/check -- CMD") || !strings.Contains(help, "/shell") {
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
	if !strings.Contains(m.notice, "contracts on") {
		t.Fatalf("notice=%q", m.notice)
	}
	m.composer.SetValue("/contract off")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if m.taskOptions.IntentContract || !strings.Contains(m.notice, "directly to Ply") {
		t.Fatalf("contract=%v notice=%q", m.taskOptions.IntentContract, m.notice)
	}
	m.composer.SetValue("/contract on")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	if !m.taskOptions.IntentContract || !strings.Contains(m.taskPolicyDisplay(), "intent contract on") {
		t.Fatalf("contract=%v policy=%q", m.taskOptions.IntentContract, m.taskPolicyDisplay())
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
	m := New(Config{Workspace: "/work", Session: "/work/.bench/sessions/task.jsonl", Model: "test/model"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if got := m.View().Content; !strings.Contains(got, "What are we working on?") || !strings.Contains(got, "FULL SHELL") {
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
	if got := m.View().Content; !strings.Contains(got, "TOOLS · END") || !strings.Contains(got, "no executable check") {
		t.Fatalf("completed task evidence is not visible:\n%s", got)
	}

	m.showHelp = true
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
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
	updated, _ := m.Update(key("enter"))
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Contract: "OUTCOME\x1b[2J\x1b]0;spoof\a\x00\nvisible", ContractDigest: "abc"})
	m = updated.(*Model)
	if len(m.messages) != 2 || m.messages[1].role != roleContract || m.messages[1].text != "OUTCOME\nvisible" {
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
