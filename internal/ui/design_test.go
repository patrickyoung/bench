package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/session"
)

type fakeDraft struct {
	newEvents   chan draftexec.Event
	checkEvents chan draftexec.Event
	buildEvents chan draftexec.Event
	proveEvents chan draftexec.Event
	newRequest  draftexec.Request
	checkDir    string
	buildDir    string
	buildModel  string
	buildAdmit  bool
	proveDir    string
}

func (f *fakeDraft) New(_ context.Context, req draftexec.Request) <-chan draftexec.Event {
	f.newRequest = req
	return f.newEvents
}

func (f *fakeDraft) Check(_ context.Context, dir string) <-chan draftexec.Event {
	f.checkDir = dir
	return f.checkEvents
}

func (f *fakeDraft) Build(_ context.Context, req draftexec.BuildRequest) <-chan draftexec.Event {
	f.buildDir = req.Dir
	f.buildModel = req.Model
	f.buildAdmit = req.Admitted
	return f.buildEvents
}

func (f *fakeDraft) Prove(_ context.Context, dir string) <-chan draftexec.Event {
	f.proveDir = dir
	return f.proveEvents
}

func TestOpenDesignPromotesUserRequirementsWithoutWriting(t *testing.T) {
	workspace := t.TempDir()
	m := New(Config{Workspace: workspace})
	m.messages = []message{
		{role: roleUser, text: "Build a patch review agent."},
		{role: roleAssistant, text: "What should prove it works?"},
		{role: roleUser, text: "A fixture suite must pass."},
	}
	m.composer.SetValue("/agent")
	updated, cmd := m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || m.screen != screenDesignForm || m.formFocus != 0 {
		t.Fatalf("design form did not open: screen=%v focus=%d", m.screen, m.formFocus)
	}
	if got := m.composer.Value(); !strings.Contains(got, "patch review") || !strings.Contains(got, "fixture suite") || strings.Contains(got, "What should") {
		t.Fatalf("requirements = %q", got)
	}
	if m.project.Value() == "" {
		t.Fatal("project path was not suggested")
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("opening the form wrote %#v", entries)
	}
}

func TestDraftNewThenCheckAdmitsOnlyExecutableVerdict(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "review-agent")
	if err := os.Mkdir(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	design := "# review-agent\n\n## Check\n\n```sh\n./bin/check\n```\n"
	if err := os.WriteFile(filepath.Join(projectDir, "DESIGN.md"), []byte(design), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := &fakeDraft{
		newEvents:   make(chan draftexec.Event),
		checkEvents: make(chan draftexec.Event),
	}
	m := New(Config{Workspace: workspace, Draft: draft, Model: "openai/design-model"})
	m.screen = screenDesignForm
	m.project.SetValue("review-agent")
	m.composer.SetValue("Review patches and prove findings with fixtures.")
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || m.job != jobDraftNew || !m.running {
		t.Fatalf("draft new did not start: job=%v running=%v", m.job, m.running)
	}
	if draft.newRequest.Dir != projectDir || !strings.Contains(draft.newRequest.Description, "fixtures") || draft.newRequest.Model != "openai/design-model" {
		t.Fatalf("new request = %#v", draft.newRequest)
	}

	updated, cmd = m.Update(draftProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if cmd == nil || m.job != jobDraftCheck || draft.checkDir != projectDir {
		t.Fatalf("draft check did not follow new: job=%v dir=%q", m.job, draft.checkDir)
	}
	updated, _ = m.Update(draftProcessEvent{Stream: draftexec.Stdout, Text: "./bin/check\n"})
	m = updated.(*Model)
	updated, _ = m.Update(draftProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.screen != screenDesignReview || !m.designBuildable || m.designCheck != "./bin/check" {
		t.Fatalf("review state: screen=%v buildable=%v check=%q", m.screen, m.designBuildable, m.designCheck)
	}
	if m.designBody != design {
		t.Fatalf("design body = %q", m.designBody)
	}
}

func TestDraftCheckExitOneIsNeedsRevisionNotBroken(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "agent")
	if err := os.Mkdir(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "DESIGN.md"), []byte("# draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(Config{Workspace: workspace})
	m.designDir = projectDir
	m.job = jobDraftCheck
	m.running = true
	m.activity = "the Check section is still false"
	updated, _ := m.Update(draftProcessEvent{Done: true, ExitCode: 1, Err: &fakeExitError{}})
	m = updated.(*Model)
	if m.screen != screenDesignReview || m.designBuildable || m.designBroken {
		t.Fatalf("exit 1 state: screen=%v buildable=%v broken=%v", m.screen, m.designBuildable, m.designBroken)
	}
	if !strings.Contains(m.notice, "Not buildable yet") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestEditorReturnRechecksTheOrdinaryDesign(t *testing.T) {
	draft := &fakeDraft{checkEvents: make(chan draftexec.Event)}
	m := New(Config{Workspace: "/work", Draft: draft})
	m.screen = screenDesignReview
	m.designDir = "/work/agent"
	updated, cmd := m.Update(editorReturnedMsg{})
	m = updated.(*Model)
	if cmd == nil || !m.running || m.job != jobDraftCheck || draft.checkDir != "/work/agent" {
		t.Fatalf("editor return: running=%v job=%v check=%q", m.running, m.job, draft.checkDir)
	}
}

func TestContractEditorErrorDoesNotMisrouteNextDesignEditorReturn(t *testing.T) {
	draft := &fakeDraft{checkEvents: make(chan draftexec.Event)}
	m := New(Config{Workspace: "/work", Draft: draft})
	m.editingContract = true
	updated, _ := m.Update(editorReturnedMsg{err: os.ErrPermission})
	m = updated.(*Model)
	if m.editingContract {
		t.Fatal("contract editor routing remained armed after an error")
	}
	m.screen = screenDesignReview
	m.designDir = "/work/agent"
	updated, cmd := m.Update(editorReturnedMsg{})
	m = updated.(*Model)
	if cmd == nil || !m.running || m.job != jobDraftCheck || draft.checkDir != "/work/agent" {
		t.Fatalf("editor return: running=%v job=%v check=%q", m.running, m.job, draft.checkDir)
	}
}

func TestExistingProjectReopensThroughDraftCheck(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "existing-agent")
	if err := os.Mkdir(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "DESIGN.md"), []byte("# Existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := &fakeDraft{checkEvents: make(chan draftexec.Event)}
	m := New(Config{Workspace: workspace, Project: projectDir, Draft: draft, Choose: true,
		Sessions: []session.Info{{Path: "/tmp/old.jsonl", Name: "old"}}})
	cmd := m.Init()
	if cmd == nil || m.picking || m.screen != screenDesignReview {
		t.Fatalf("project init: cmd=%v picking=%v screen=%v", cmd != nil, m.picking, m.screen)
	}
	updated, wait := m.Update(cmd())
	m = updated.(*Model)
	if wait == nil || !m.running || m.job != jobDraftCheck || draft.checkDir != projectDir {
		t.Fatalf("project check: running=%v job=%v dir=%q", m.running, m.job, draft.checkDir)
	}
	updated, _ = m.Update(draftProcessEvent{Stream: draftexec.Stdout, Text: "./bin/check\n"})
	m = updated.(*Model)
	updated, _ = m.Update(draftProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if !m.designBuildable || m.designBody != "# Existing\n" {
		t.Fatalf("reopened project: buildable=%v body=%q", m.designBuildable, m.designBody)
	}
}

func TestProjectPathCannotEscapeWorkspace(t *testing.T) {
	workspace := t.TempDir()
	if _, err := ProjectPath(workspace, "../outside"); err == nil {
		t.Fatal("accepted project outside workspace")
	}
	if got, err := ProjectPath(workspace, "inside"); err != nil || got != filepath.Join(workspace, "inside") {
		t.Fatalf("inside path = %q, %v", got, err)
	}
}

func TestProjectPathCannotEscapeThroughSymlinkedParent(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ProjectPath(workspace, filepath.Join("linked", "agent")); err == nil {
		t.Fatal("accepted project through an escaping symlink")
	}
}

func TestHomePathUsesWorkspaceContainment(t *testing.T) {
	workspace := t.TempDir()
	home := filepath.Join(workspace, "support-chief")
	if got, err := HomePath(workspace, "support-chief"); err != nil || got != home {
		t.Fatalf("home path = %q, %v", got, err)
	}
	if _, err := HomePath(workspace, "../outside"); err == nil || !strings.Contains(err.Error(), "agent home") {
		t.Fatalf("outside home error = %v", err)
	}
}

func TestProjectSlugIsBoringAndPortable(t *testing.T) {
	if got := projectSlug("Build: A Go Patch_Review Agent!!!"); got != "build-a-go-patch-review-agent" {
		t.Fatalf("slug = %q", got)
	}
}

func TestDesignScreensFitEightyByTwentyFour(t *testing.T) {
	m := New(Config{Workspace: "/work/project"})
	m.messages = []message{{role: roleUser, text: "Build a small agent with a real check."}}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.composer.SetValue("/agent")
	updated, _ = m.Update(key("enter"))
	m = updated.(*Model)
	assertTerminalBounds(t, m.View().Content, 80, 24)

	m.screen = screenDesignReview
	m.designDir = "/work/project/review-agent"
	m.designBody = strings.Repeat("# Heading\n\nA requirement with enough words to wrap safely.\n\n", 20)
	m.designBuildable = true
	m.designCheck = "./bin/check"
	m.viewport.GotoTop()
	m.syncContent()
	view := m.View()
	assertTerminalBounds(t, view.Content, 80, 24)
	if !strings.Contains(view.Content, "BUILDABLE") {
		t.Fatalf("review did not render verdict: %q", view.Content)
	}
}

func assertTerminalBounds(t *testing.T, content string, width, height int) {
	t.Helper()
	if got := lipgloss.Height(content); got > height {
		t.Fatalf("height = %d, want <= %d", got, height)
	}
	for i, line := range strings.Split(content, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i+1, got, width, line)
		}
	}
}
