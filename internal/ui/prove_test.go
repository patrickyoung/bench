package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/draftexec"
)

func TestProveStartsOnlyFromPassedBuildAndUsesExitZeroVerdict(t *testing.T) {
	draft := &fakeDraft{proveEvents: make(chan draftexec.Event)}
	m := New(Config{Workspace: t.TempDir(), Draft: draft})
	m.screen = screenBuild
	m.designDir = "/work/review-agent"
	m.buildState = buildPassed

	updated, cmd := m.Update(key("p"))
	m = updated.(*Model)
	if cmd == nil || !m.running || m.screen != screenProve || draft.proveDir != m.designDir {
		t.Fatalf("prove did not start: running=%v screen=%v dir=%q", m.running, m.screen, draft.proveDir)
	}
	updated, _ = m.Update(draftProcessEvent{Stream: draftexec.Stderr, Text: "killed 3 of 3\n"})
	m = updated.(*Model)
	updated, _ = m.Update(draftProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.running || m.proveState != provePassed || !strings.Contains(m.proveLog, "3 of 3") {
		t.Fatalf("prove state=%v running=%v log=%q", m.proveState, m.running, m.proveLog)
	}
}

func TestProveExitOneMeansGapsNotProcessFailure(t *testing.T) {
	m := New(Config{})
	m.screen = screenProve
	m.running = true
	m.job = jobDraftProve
	m.proveState = proveRunning
	updated, _ := m.Update(draftProcessEvent{Stream: draftexec.Stdout, Text: "agent.go:9 survived\n"})
	m = updated.(*Model)
	updated, _ = m.Update(draftProcessEvent{Done: true, ExitCode: 1, Err: &fakeExitError{}})
	m = updated.(*Model)
	if m.proveState != proveGaps || !strings.Contains(m.proveFindings, "survived") {
		t.Fatalf("prove state=%v findings=%q", m.proveState, m.proveFindings)
	}
}

func TestProveExitTwoIsFailure(t *testing.T) {
	m := New(Config{})
	m.screen = screenProve
	m.running = true
	m.job = jobDraftProve
	m.proveState = proveRunning
	updated, _ := m.Update(draftProcessEvent{Done: true, ExitCode: 2, Err: &fakeExitError{}})
	m = updated.(*Model)
	if m.proveState != proveFailed || !strings.Contains(m.notice, "draft prove") {
		t.Fatalf("prove state=%v notice=%q", m.proveState, m.notice)
	}
}

func TestProveOutputTruncationAnnouncesItself(t *testing.T) {
	m := New(Config{})
	m.proveLog = appendVisibleOutput("", strings.Repeat("x", proveOutputLimit+100), proveOutputLimit,
		"[earlier measurement output omitted]\n")
	if !strings.Contains(m.proveLog, "omitted") || len([]rune(m.proveLog)) != proveOutputLimit {
		t.Fatalf("truncated output was not explicit: len=%d", len([]rune(m.proveLog)))
	}
}

func TestProveScreenFitsEightyByTwentyFour(t *testing.T) {
	m := New(Config{Workspace: "/work"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.screen = screenProve
	m.designDir = "/work/review-agent"
	m.proveState = provePassed
	m.proveLog = strings.Repeat("killed mutation with a deliberately long explanation\n", 30)
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "CHECK PROVEN") {
		t.Fatalf("prove verdict not rendered: %q", m.View().Content)
	}
}
