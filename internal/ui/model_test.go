package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/session"
)

type fakeRunner struct {
	events       chan askexec.Event
	replayEvents chan askexec.Event
	req          askexec.Request
	replayPath   string
}

func (f *fakeRunner) Replay(_ context.Context, path string) <-chan askexec.Event {
	f.replayPath = path
	return f.replayEvents
}

func (f *fakeRunner) Start(_ context.Context, req askexec.Request) <-chan askexec.Event {
	f.req = req
	return f.events
}

func TestSubmitAndSuccessfulTurn(t *testing.T) {
	runner := &fakeRunner{events: make(chan askexec.Event, 3)}
	m := New(Config{Runner: runner, Session: "/tmp/run.jsonl", Model: "test/model", InitialPrompt: "build it"})
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || !m.running {
		t.Fatal("submit did not start a turn")
	}
	if runner.req.Message != "build it" || runner.req.Session != "/tmp/run.jsonl" {
		t.Fatalf("request = %#v", runner.req)
	}

	updated, _ = m.Update(processEvent{Stream: askexec.Stderr, Text: "thinking"})
	m = updated.(*Model)
	updated, _ = m.Update(processEvent{Stream: askexec.Stdout, Text: "working agent"})
	m = updated.(*Model)
	updated, _ = m.Update(processEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)

	if m.running || len(m.messages) != 2 {
		t.Fatalf("running=%v messages=%#v", m.running, m.messages)
	}
	if got := m.messages[1].text; got != "working agent" {
		t.Fatalf("answer = %q", got)
	}
	if !strings.Contains(m.notice, "replayable") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestSubmitPassesOnlyExplicitlyActiveBriefSkills(t *testing.T) {
	runner := &fakeRunner{events: make(chan askexec.Event)}
	m := New(Config{Runner: runner, Session: "/tmp/run.jsonl", InitialPrompt: "review it"})
	m.activeSkills = []string{"go-review", "house-style"}
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || !m.running {
		t.Fatal("skilled turn did not start")
	}
	if got := strings.Join(runner.req.Skills, ","); got != "go-review,house-style" {
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
	if !view.AltScreen || !strings.Contains(view.Content, "REQUIREMENTS") {
		t.Fatalf("view missing terminal contract: %#v", view)
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
	if !strings.Contains(content, "SESSION") || strings.Contains(content, "Start with requirements") {
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
