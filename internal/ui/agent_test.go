package ui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/agentexec"
)

type fakeAgent struct {
	events chan agentexec.Event
	args   [][]string
	stdin  []string
}

func (f *fakeAgent) Start(_ context.Context, args []string, stdin string) <-chan agentexec.Event {
	f.args = append(f.args, append([]string(nil), args...))
	f.stdin = append(f.stdin, stdin)
	f.events = make(chan agentexec.Event)
	return f.events
}

func TestExistingAgentHomeOpensThroughPublicShow(t *testing.T) {
	agent := &fakeAgent{}
	home := "/work/support-chief"
	m := New(Config{Workspace: "/work", Home: home, Agent: agent, Choose: true, Model: "openai/agent-model"})
	cmd := m.Init()
	if cmd == nil || m.picking || m.screen != screenAgentHome {
		t.Fatalf("home init: cmd=%v picking=%v screen=%v", cmd != nil, m.picking, m.screen)
	}
	updated, wait := m.Update(cmd())
	m = updated.(*Model)
	if wait == nil || !m.running || m.job != jobAgent || len(agent.args) != 1 || !slices.Equal(agent.args[0], []string{"show", home}) {
		t.Fatalf("show start: running=%v job=%v args=%#v", m.running, m.job, agent.args)
	}

	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stdout, Text: "agent-home: /work/support-chief\ncompiled-sha256: abc\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stderr, Text: "agent: valid home\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.running || m.agentState != agentSucceeded || !strings.Contains(m.renderAgentHome(76), "compiled-sha256") {
		t.Fatalf("show result: running=%v state=%v body=%q", m.running, m.agentState, m.agentDefinition)
	}
}

func TestAgentHomeActionsRemainPublicLiteralCommands(t *testing.T) {
	agent := &fakeAgent{}
	home := "/work/home with spaces"
	m := New(Config{Workspace: "/work", Home: home, Agent: agent, Model: "provider/model"})

	updated, _ := m.Update(key("g"))
	m = updated.(*Model)
	if got := agent.args[len(agent.args)-1]; !slices.Equal(got, []string{"run", "-m", "provider/model", home}) {
		t.Fatalf("run args = %#v", got)
	}
	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stdout, Text: "worker answer\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 1})
	m = updated.(*Model)
	if m.agentState != agentNegative || !strings.Contains(m.notice, "Not accepted") {
		t.Fatalf("negative run state=%v notice=%q", m.agentState, m.notice)
	}

	updated, _ = m.Update(key("l"))
	m = updated.(*Model)
	if got := agent.args[len(agent.args)-1]; !slices.Equal(got, []string{"history", home}) {
		t.Fatalf("history args = %#v", got)
	}
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	updated, _ = m.Update(key("p"))
	m = updated.(*Model)
	if got := agent.args[len(agent.args)-1]; !slices.Equal(got, []string{"proposals", home}) {
		t.Fatalf("proposal review args = %#v", got)
	}
	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stdout, Text: "agent-proposals/v1\ncount: 1\npatch-bytes:\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.agentState != agentSucceeded || !strings.Contains(m.renderAgentHome(76), "PROPOSALS · READ ONLY") {
		t.Fatalf("proposal result: state=%v body=%q", m.agentState, m.agentOutput)
	}
	updated, _ = m.Update(key("v"))
	m = updated.(*Model)
	if got := agent.args[len(agent.args)-1]; !slices.Equal(got, []string{"history", home, "check"}) {
		t.Fatalf("history check args = %#v", got)
	}
	for i, input := range agent.stdin {
		if input != "" {
			t.Fatalf("call %d injected stdin %q", i, input)
		}
	}
}

func TestAgentHomeBrokenStatusIsNotSuccess(t *testing.T) {
	m := New(Config{Workspace: "/work", Home: "/work/broken", Agent: &fakeAgent{}})
	m.running = true
	m.job = jobAgent
	m.agentCommand = "check"
	updated, _ := m.Update(agentProcessEvent{Stream: agentexec.Stderr, Text: "missing bin/check\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 2})
	m = updated.(*Model)
	if m.agentState != agentBroken || !strings.Contains(m.notice, "missing bin/check") {
		t.Fatalf("state=%v notice=%q activity=%q", m.agentState, m.notice, m.agentActivity)
	}
}

func TestAgentHomeScreenFitsEightyByTwentyFour(t *testing.T) {
	home := filepath.Join("/work", "support-chief")
	m := New(Config{Workspace: "/work", Home: home, Agent: &fakeAgent{}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.agentCommand = "show"
	m.agentDefinition = strings.Repeat("# Home\nA bounded compiled definition line.\n", 30)
	m.agentState = agentSucceeded
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "VALID") {
		t.Fatalf("agent verdict missing: %q", m.View().Content)
	}
	updated, _ = m.Update(key("f1"))
	m = updated.(*Model)
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "Agent home keyboard") {
		t.Fatalf("agent help missing: %q", m.View().Content)
	}
}
