package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/honeexec"
)

type fakeHone struct {
	events  chan honeexec.Event
	request honeexec.Request
}

func (f *fakeHone) Learn(_ context.Context, req honeexec.Request) <-chan honeexec.Event {
	f.request = req
	return f.events
}

func TestLearnUsesBuildEvidenceAndAdmitsOnlyExitZero(t *testing.T) {
	hone := &fakeHone{events: make(chan honeexec.Event)}
	m := New(Config{Workspace: t.TempDir(), Hone: hone})
	m.screen = screenProve
	m.proveState = provePassed
	m.designDir = "/work/review-agent"
	m.buildSession = "/work/review-agent/.draft/build/run.jsonl"

	updated, cmd := m.Update(key("l"))
	m = updated.(Model)
	if cmd == nil || m.screen != screenLearn || m.skill.Value() != "review-agent" {
		t.Fatalf("learn screen=%v skill=%q", m.screen, m.skill.Value())
	}
	updated, cmd = m.Update(key("ctrl+s"))
	m = updated.(Model)
	if cmd == nil || !m.running || hone.request.Session != m.buildSession || hone.request.Skill != "review-agent" {
		t.Fatalf("learn did not start: running=%v request=%#v", m.running, hone.request)
	}
	updated, _ = m.Update(honeProcessEvent{Stream: honeexec.Stderr, Text: "run: 2 stumbles, check passed\n"})
	m = updated.(Model)
	updated, _ = m.Update(honeProcessEvent{Done: true, ExitCode: 0})
	m = updated.(Model)
	if m.running || m.learnState != learned || !strings.Contains(m.learnLog, "check passed") {
		t.Fatalf("learn state=%v running=%v log=%q", m.learnState, m.running, m.learnLog)
	}
}

func TestLearnExitOneMeansNothingWasAdmitted(t *testing.T) {
	m := New(Config{})
	m.screen = screenLearn
	m.running = true
	m.job = jobHone
	m.learnState = learnRunning
	updated, _ := m.Update(honeProcessEvent{Done: true, ExitCode: 1, Err: &fakeExitError{}})
	m = updated.(Model)
	if m.learnState != learnNothing || !strings.Contains(m.notice, "Nothing learned") {
		t.Fatalf("learn state=%v notice=%q", m.learnState, m.notice)
	}
}

func TestLearnRequiresProvenEvaluation(t *testing.T) {
	m := New(Config{Hone: &fakeHone{}})
	m.screen = screenProve
	m.proveState = proveGaps
	updated, cmd := m.Update(key("l"))
	m = updated.(Model)
	if cmd != nil || m.screen != screenProve || !strings.Contains(m.notice, "proven") {
		t.Fatalf("learn gate: screen=%v notice=%q", m.screen, m.notice)
	}
}

func TestLearnScreenFitsEightyByTwentyFour(t *testing.T) {
	m := New(Config{Workspace: "/work"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.screen = screenLearn
	m.designDir = "/work/review-agent"
	m.buildSession = "/work/review-agent/.draft/build/a-very-long-session-name.jsonl"
	m.skill.SetValue("review-agent-house")
	m.learnState = learned
	m.learnLog = strings.Repeat("verified provenance with a useful recovery\n", 30)
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "LESSON ADMITTED") {
		t.Fatalf("learn verdict not rendered: %q", m.View().Content)
	}
}
