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

func TestAgentSpecialistFormInvokesOneLiteralChildTask(t *testing.T) {
	agent := &fakeAgent{}
	home := "/work/home with spaces"
	m := New(Config{Workspace: "/work", Home: home, Agent: agent, Model: "provider/model"})

	updated, _ := m.Update(key("s"))
	m = updated.(*Model)
	if !m.agentChildOpen || m.agentChildFocus != 0 || !m.agentChild.Focused() {
		t.Fatalf("specialist form open=%v focus=%d input-focused=%v", m.agentChildOpen, m.agentChildFocus, m.agentChild.Focused())
	}
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	if len(agent.args) != 0 || !strings.Contains(m.notice, "specialist name") {
		t.Fatalf("empty specialist submitted args=%#v notice=%q", agent.args, m.notice)
	}

	m.agentChild.SetValue("researcher")
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	if len(agent.args) != 0 || !strings.Contains(m.notice, "bounded task") {
		t.Fatalf("empty task submitted args=%#v notice=%q", agent.args, m.notice)
	}
	updated, _ = m.Update(key("tab"))
	m = updated.(*Model)
	task := "Compare the two fixtures.\nReturn only evidence and one recommendation."
	m.composer.SetValue(task)
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	want := []string{"specialist", home, "researcher", "-m", "provider/model"}
	if len(agent.args) != 1 || !slices.Equal(agent.args[0], want) {
		t.Fatalf("specialist args = %#v, want %#v", agent.args, want)
	}
	if m.agentChildOpen || !m.running || m.job != jobAgent || agent.stdin[0] != task {
		t.Fatalf("specialist start open=%v running=%v job=%v stdin=%q", m.agentChildOpen, m.running, m.job, agent.stdin)
	}

	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stdout, Text: "bounded child result\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	view := m.renderAgentHome(76)
	if m.agentState != agentSucceeded || !strings.Contains(view, "SPECIALIST RESULT") || !strings.Contains(view, "SPECIALIST CHECK ACCEPTED") {
		t.Fatalf("specialist result state=%v view=%q", m.agentState, view)
	}
}

func TestAgentSpecialistFormCancelsWithoutInvokingChild(t *testing.T) {
	agent := &fakeAgent{}
	m := New(Config{Workspace: "/work", Home: "/work/chief", Agent: agent})
	updated, _ := m.Update(key("s"))
	m = updated.(*Model)
	m.agentChild.SetValue("researcher")
	m.composer.SetValue("do not run")
	updated, cmd := m.Update(key("esc"))
	m = updated.(*Model)
	if cmd != nil || m.agentChildOpen || len(agent.args) != 0 || !strings.Contains(m.notice, "cancelled") {
		t.Fatalf("cancel cmd=%v open=%v args=%#v notice=%q", cmd != nil, m.agentChildOpen, agent.args, m.notice)
	}
}

func TestRunningAgentSpecialistCanBeInterrupted(t *testing.T) {
	agent := &fakeAgent{}
	m := New(Config{Workspace: "/work", Home: "/work/chief", Agent: agent})
	updated, _ := m.Update(key("s"))
	m = updated.(*Model)
	m.agentChild.SetValue("researcher")
	m.composer.SetValue("one bounded task")
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	updated, _ = m.Update(key("esc"))
	m = updated.(*Model)
	if !m.running || !strings.Contains(m.notice, "Interrupting agent") {
		t.Fatalf("interrupt running=%v notice=%q", m.running, m.notice)
	}
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 130, Err: context.Canceled})
	m = updated.(*Model)
	if m.running || m.agentState != agentInterrupted || !strings.Contains(m.notice, "interrupted") {
		t.Fatalf("interrupted running=%v state=%v notice=%q", m.running, m.agentState, m.notice)
	}
}

func TestAgentLearningFormReviewsEvidenceWithoutLearning(t *testing.T) {
	agent := &fakeAgent{}
	home := "/work/home with spaces"
	m := New(Config{Workspace: "/work", Home: home, Agent: agent, Model: "provider/model"})

	updated, _ := m.Update(key("h"))
	m = updated.(*Model)
	if !m.agentLearnOpen || m.agentLearnFocus != 0 || !m.agentLearnSkill.Focused() {
		t.Fatalf("learn form open=%v focus=%d skill-focused=%v", m.agentLearnOpen, m.agentLearnFocus, m.agentLearnSkill.Focused())
	}
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	if len(agent.args) != 0 || !strings.Contains(m.notice, "destination skill") {
		t.Fatalf("empty learn form submitted args=%#v notice=%q", agent.args, m.notice)
	}

	m.agentLearnSkill.SetValue("triage")
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	if len(agent.args) != 0 || !strings.Contains(m.notice, "replayable session") {
		t.Fatalf("empty learn session submitted args=%#v notice=%q", agent.args, m.notice)
	}
	updated, _ = m.Update(key("tab"))
	m = updated.(*Model)
	m.agentLearnRun.SetValue("recovery with spaces.jsonl")
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	want := []string{"learn", "-into", "triage", "-why", home, "recovery with spaces.jsonl"}
	if len(agent.args) != 1 || !slices.Equal(agent.args[0], want) || agent.stdin[0] != "" {
		t.Fatalf("learn review args=%#v stdin=%#v want=%#v", agent.args, agent.stdin, want)
	}

	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stdout, Text: "GOAL\nfix it\n\nCHECK\nmake check\n\nSTUMBLE 1\nfailed then passed\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	view := m.renderAgentHome(76)
	if m.agentState != agentSucceeded || !strings.Contains(view, "LEARNING EVIDENCE · READ ONLY") || !strings.Contains(view, "LEARNING EVIDENCE REVIEWED") {
		t.Fatalf("learning review state=%v view=%q", m.agentState, view)
	}
	lastCommand := m.agentCommandDisplay()
	updated, _ = m.Update(key("h"))
	m = updated.(*Model)
	m.agentLearnSkill.SetValue("different-skill")
	m.agentLearnRun.SetValue("different-run.jsonl")
	if got := m.agentCommandDisplay(); got != lastCommand {
		t.Fatalf("editing a new review changed prior command display: got %q want %q", got, lastCommand)
	}
}

func TestAgentLearningReviewPreservesNothingToLearnVerdict(t *testing.T) {
	agent := &fakeAgent{}
	m := New(Config{Workspace: "/work", Home: "/work/chief", Agent: agent})
	m.agentLearnName = "triage"
	m.agentLearnSession = "clean-run.jsonl"
	updated, _ := m.startAgentCommand("learn-why")
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 1})
	m = updated.(*Model)
	if m.agentState != agentNegative || !strings.Contains(m.notice, "no replay-verified recovery") {
		t.Fatalf("learning negative state=%v notice=%q", m.agentState, m.notice)
	}
}

func TestAgentAmendmentFormPreservesExactMayStates(t *testing.T) {
	agent := &fakeAgent{}
	home := "/work/home with spaces"
	m := New(Config{Workspace: "/work", Home: home, Agent: agent})

	updated, _ := m.Update(key("a"))
	m = updated.(*Model)
	if !m.agentAmendOpen || !m.agentAmend.Focused() {
		t.Fatalf("amend form open=%v focused=%v", m.agentAmendOpen, m.agentAmend.Focused())
	}
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	if len(agent.args) != 0 || !strings.Contains(m.notice, "reviewed .patch") {
		t.Fatalf("empty amendment submitted args=%#v notice=%q", agent.args, m.notice)
	}

	m.agentAmend.SetValue("tighten-checking.patch")
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	want := []string{"amend", home, "tighten-checking.patch"}
	if len(agent.args) != 1 || !slices.Equal(agent.args[0], want) || agent.stdin[0] != "" {
		t.Fatalf("amend args=%#v stdin=%#v want=%#v", agent.args, agent.stdin, want)
	}
	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stdout, Text: "{\"digest\":\"sha256:exact\",\"verdict\":\"parked\"}\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 75})
	m = updated.(*Model)
	view := m.renderAgentHome(76)
	if m.agentState != agentApprovalPending || !strings.Contains(view, "AMENDMENT APPROVAL / RESULT") || !strings.Contains(view, "APPROVAL PARKED") || !strings.Contains(m.notice, "definition is unchanged") {
		t.Fatalf("parked amendment state=%v notice=%q view=%q", m.agentState, m.notice, view)
	}

	lastCommand := m.agentCommandDisplay()
	updated, _ = m.Update(key("a"))
	m = updated.(*Model)
	m.agentAmend.SetValue("different.patch")
	if got := m.agentCommandDisplay(); got != lastCommand {
		t.Fatalf("editing a new amendment changed prior command display: got %q want %q", got, lastCommand)
	}
	updated, _ = m.Update(key("esc"))
	m = updated.(*Model)
	updated, _ = m.Update(key("a"))
	m = updated.(*Model)
	if got := m.agentAmend.Value(); got != "tighten-checking.patch" {
		t.Fatalf("parked amendment retry value=%q", got)
	}
	updated, _ = m.Update(key("ctrl+enter"))
	m = updated.(*Model)
	if len(agent.args) != 2 || !slices.Equal(agent.args[1], want) {
		t.Fatalf("amend retry args=%#v want=%#v", agent.args, want)
	}
	m.agentDefinition = "stale compiled home"
	updated, _ = m.Update(agentProcessEvent{Stream: agentexec.Stderr, Text: "agent: amended GOAL.md · evidence=receipt\n"})
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.agentState != agentSucceeded || m.agentDefinition != "" || !strings.Contains(m.notice, "press r") || !strings.Contains(m.renderAgentHome(76), "AMENDMENT APPLIED") {
		t.Fatalf("applied amendment state=%v definition=%q notice=%q view=%q", m.agentState, m.agentDefinition, m.notice, m.renderAgentHome(76))
	}
}

func TestAgentAmendmentPreservesMayDecline(t *testing.T) {
	m := New(Config{Workspace: "/work", Home: "/work/chief", Agent: &fakeAgent{}})
	m.agentAmendName = "proposal.patch"
	updated, _ := m.startAgentCommand("amend")
	m = updated.(*Model)
	updated, _ = m.Update(agentProcessEvent{Done: true, ExitCode: 3})
	m = updated.(*Model)
	if m.agentState != agentNegative || !strings.Contains(m.notice, "declined") || !strings.Contains(m.renderAgentHome(76), "AMENDMENT DECLINED") {
		t.Fatalf("declined amendment state=%v notice=%q view=%q", m.agentState, m.notice, m.renderAgentHome(76))
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
	m.showHelp = false
	updated, _ = m.Update(key("s"))
	m = updated.(*Model)
	m.agentChild.SetValue("researcher")
	m.composer.SetValue("one bounded task")
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "DIRECT SPECIALIST HOME") || !strings.Contains(m.View().Content, "SPECIALIST TASK") {
		t.Fatalf("specialist form missing: %q", m.View().Content)
	}
	updated, _ = m.closeAgentSpecialistForm()
	m = updated.(*Model)
	updated, _ = m.Update(key("h"))
	m = updated.(*Model)
	m.agentLearnSkill.SetValue("triage")
	m.agentLearnRun.SetValue("recovery.jsonl")
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "DESTINATION SKILL") || !strings.Contains(m.View().Content, "VERIFIED LEARNING EVIDENCE") {
		t.Fatalf("learning form missing: %q", m.View().Content)
	}
	updated, _ = m.closeAgentLearnForm()
	m = updated.(*Model)
	updated, _ = m.Update(key("a"))
	m = updated.(*Model)
	m.agentAmend.SetValue("tighten-checking.patch")
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
	if !strings.Contains(m.View().Content, "REVIEWED PATCH FILE") || !strings.Contains(m.View().Content, "MAY-GATED AMENDMENT") {
		t.Fatalf("amendment form missing: %q", m.View().Content)
	}
}
