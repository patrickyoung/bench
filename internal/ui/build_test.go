package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/draftexec"
)

func TestBuildSuccessComesFromExitZeroAndKeepsEvidence(t *testing.T) {
	workspace := t.TempDir()
	project := filepath.Join(workspace, "agent")
	evidence := filepath.Join(project, ".draft", "build")
	if err := os.MkdirAll(evidence, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "run.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := &fakeDraft{buildEvents: make(chan draftexec.Event)}
	m := New(Config{Workspace: workspace, Draft: draft, Model: "anthropic/build-model"})
	m.designDir = project
	m.designBuildable = true
	m.screen = screenDesignReview
	updated, cmd := m.Update(key("b"))
	m = updated.(*Model)
	if cmd == nil || !m.running || m.job != jobDraftBuild || draft.buildDir != project || draft.buildModel != "anthropic/build-model" || draft.buildAdmit {
		t.Fatalf("build did not start: running=%v job=%v dir=%q model=%q", m.running, m.job, draft.buildDir, draft.buildModel)
	}
	updated, _ = m.Update(draftProcessEvent{Stream: draftexec.Stderr, Text: "$ go test ./...\nok\n"})
	m = updated.(*Model)
	updated, _ = m.Update(draftProcessEvent{Stream: draftexec.Stdout, Text: "Built it.\n"})
	m = updated.(*Model)
	updated, _ = m.Update(draftProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.running || m.buildState != buildPassed {
		t.Fatalf("build state = %v, running=%v", m.buildState, m.running)
	}
	if !strings.Contains(m.buildLog, "go test") || m.buildAnswer != "Built it.\n" {
		t.Fatalf("log=%q answer=%q", m.buildLog, m.buildAnswer)
	}
	if m.buildSession != filepath.Join(evidence, "run.jsonl") {
		t.Fatalf("evidence = %q", m.buildSession)
	}
}

func TestAdmittedBuildIsExplicitAndRetryKeepsBoundary(t *testing.T) {
	draft := &fakeDraft{buildEvents: make(chan draftexec.Event)}
	m := New(Config{Workspace: "/work", Draft: draft})
	m.designDir = "/work/agent"
	m.designBuildable = true
	m.screen = screenDesignReview
	updated, cmd := m.Update(key("B"))
	m = updated.(*Model)
	if cmd == nil || !m.running || !draft.buildAdmit || !m.buildAdmitted {
		t.Fatalf("admitted build did not start: running=%v req=%v state=%v", m.running, draft.buildAdmit, m.buildAdmitted)
	}
	updated, _ = m.Update(draftProcessEvent{Done: true, ExitCode: 1, Err: &fakeExitError{}})
	m = updated.(*Model)
	if !strings.Contains(m.View().Content, "draft build -admitted /work/agent") {
		t.Fatalf("admitted boundary is not visible: %q", m.View().Content)
	}
	draft.buildEvents = make(chan draftexec.Event)
	updated, cmd = m.Update(key("r"))
	m = updated.(*Model)
	if cmd == nil || !draft.buildAdmit {
		t.Fatal("retry downgraded the admitted build")
	}
}

func TestBuildExitTwoIsNotDoneNotBroken(t *testing.T) {
	m := New(Config{})
	m.screen = screenBuild
	m.running = true
	m.job = jobDraftBuild
	m.buildState = buildRunning
	updated, _ := m.Update(draftProcessEvent{Done: true, ExitCode: 2, Err: &fakeExitError{}})
	m = updated.(*Model)
	if m.buildState != buildNotDone {
		t.Fatalf("build state = %v", m.buildState)
	}
	if !strings.Contains(m.notice, "Not done") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestBuildLogTruncationAnnouncesItself(t *testing.T) {
	m := New(Config{})
	m.appendBuildLog(strings.Repeat("x", buildLogLimit+100))
	if !strings.Contains(m.buildLog, "omitted from this view") {
		t.Fatal("build log was truncated silently")
	}
	if len([]rune(m.buildLog)) != buildLogLimit {
		t.Fatalf("log length = %d", len([]rune(m.buildLog)))
	}
}
