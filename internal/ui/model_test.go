package ui

import (
	"context"
	"strings"
	"testing"

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
	events chan plyexec.Event
	req    plyexec.TaskRequest
}

func (f *fakeTask) Work(_ context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	f.req = req
	return f.events
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
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || !m.running {
		t.Fatal("submit did not start a task")
	}
	if task.req.Goal != "build it" || task.req.Session != "/tmp/run.jsonl" || task.req.Dir != "/work" {
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
	m := New(Config{Task: task, Session: "/tmp/run.jsonl", Workspace: "/work", InitialPrompt: "finish it"})
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
}

func TestCtrlTTogglesToAskOnly(t *testing.T) {
	runner := &fakeRunner{events: make(chan askexec.Event)}
	m := New(Config{Runner: runner, Session: "/tmp/run.jsonl", InitialPrompt: "explain it"})
	updated, _ := m.Update(key("ctrl+t"))
	m = updated.(*Model)
	if m.taskMode || !strings.Contains(m.notice, "Ask only") {
		t.Fatalf("taskMode=%v notice=%q", m.taskMode, m.notice)
	}
	if got := m.View(); got.WindowTitle != "bench · ask" || !strings.Contains(got.Content, "ASK ONLY · NO TOOLS") {
		t.Fatalf("Ask-only grant is not visible: title=%q\n%s", got.WindowTitle, got.Content)
	}
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || m.job != jobTurn || runner.req.Message != "explain it" {
		t.Fatalf("job=%v request=%#v", m.job, runner.req)
	}
}

func TestSubmitPassesOnlyExplicitlyActiveBriefSkills(t *testing.T) {
	runner := &fakeRunner{events: make(chan askexec.Event)}
	task := &fakeTask{events: make(chan plyexec.Event)}
	m := New(Config{Task: task, Runner: runner, Session: "/tmp/run.jsonl", InitialPrompt: "review it"})
	m.activeSkills = []string{"go-review", "house-style"}
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
	if !view.AltScreen || view.WindowTitle != "bench · task" || !strings.Contains(view.Content, "TASK · FULL SHELL") {
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
}

func TestExplicitToolboxIsVisibleInsteadOfFullShell(t *testing.T) {
	m := New(Config{Workspace: "/work", Toolbox: "/work/.bench/tools"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	got := m.View().Content
	if !strings.Contains(got, "TASK · TOOLBOX tools") || strings.Contains(got, "TASK · FULL SHELL") {
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
