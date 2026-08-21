package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type fakeBrief struct {
	listEvents  chan briefexec.Event
	pathEvents  chan briefexec.Event
	catEvents   chan briefexec.Event
	filesEvents chan briefexec.Event
	lintEvents  chan briefexec.Event
	newEvents   chan briefexec.Event
	pathRef     string
	catRef      string
	filesRef    string
	lintTarget  string
	newRequest  briefexec.NewRequest
}

func (f *fakeBrief) List(context.Context) <-chan briefexec.Event { return f.listEvents }
func (f *fakeBrief) Path(_ context.Context, ref string) <-chan briefexec.Event {
	f.pathRef = ref
	return f.pathEvents
}
func (f *fakeBrief) Cat(_ context.Context, ref string) <-chan briefexec.Event {
	f.catRef = ref
	return f.catEvents
}
func (f *fakeBrief) Files(_ context.Context, ref string) <-chan briefexec.Event {
	f.filesRef = ref
	return f.filesEvents
}
func (f *fakeBrief) Lint(_ context.Context, target string) <-chan briefexec.Event {
	f.lintTarget = target
	return f.lintEvents
}
func (f *fakeBrief) New(_ context.Context, req briefexec.NewRequest) <-chan briefexec.Event {
	f.newRequest = req
	return f.newEvents
}

type fakePly struct {
	events  chan plyexec.Event
	request plyexec.RefineRequest
}

func (f *fakePly) Refine(_ context.Context, req plyexec.RefineRequest) <-chan plyexec.Event {
	f.request = req
	return f.events
}

func TestSkillsCatalogueLoadsPublicBriefMetadataAndDetail(t *testing.T) {
	workspace := t.TempDir()
	brief := &fakeBrief{
		listEvents: make(chan briefexec.Event), pathEvents: make(chan briefexec.Event),
		catEvents: make(chan briefexec.Event), filesEvents: make(chan briefexec.Event), lintEvents: make(chan briefexec.Event),
	}
	m := New(Config{Workspace: workspace, Brief: brief})
	updated, cmd := m.Update(key("ctrl+b"))
	m = updated.(*Model)
	if cmd == nil || !m.running || m.job != jobBriefList || m.screen != screenSkills {
		t.Fatalf("catalogue did not start: running=%v job=%v screen=%v", m.running, m.job, m.screen)
	}
	updated, _ = m.Update(briefProcessEvent{Stream: briefexec.Stdout, Text: "go-review\tReview Go patches with fixtures.\nweb-perf\tMeasure Core Web Vitals.\n"})
	m = updated.(*Model)
	updated, _ = m.Update(briefProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if len(m.skills) != 2 || m.skills[0].Name != "go-review" {
		t.Fatalf("skills = %#v", m.skills)
	}

	updated, cmd = m.Update(key("enter"))
	m = updated.(*Model)
	if cmd == nil || m.job != jobBriefPath || brief.pathRef != "go-review" {
		t.Fatalf("path lookup: job=%v ref=%q", m.job, brief.pathRef)
	}
	skillDir := filepath.Join(workspace, ".claude", "skills", "go-review")
	updated, _ = m.Update(briefProcessEvent{Stream: briefexec.Stdout, Text: skillDir + "\n"})
	m = updated.(*Model)
	updated, cmd = m.Update(briefProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if cmd == nil || m.job != jobBriefCat || brief.catRef != filepath.Join(skillDir, "SKILL.md") {
		t.Fatalf("cat lookup: job=%v ref=%q", m.job, brief.catRef)
	}
	body := "---\nname: go-review\ndescription: Review Go patches.\n---\n\n# Steps\n"
	updated, _ = m.Update(briefProcessEvent{Stream: briefexec.Stdout, Text: body})
	m = updated.(*Model)
	updated, cmd = m.Update(briefProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if cmd == nil || m.job != jobBriefFiles || brief.filesRef != skillDir {
		t.Fatalf("files lookup: job=%v ref=%q", m.job, brief.filesRef)
	}
	updated, _ = m.Update(briefProcessEvent{Stream: briefexec.Stdout, Text: "SKILL.md\nreferences/CHECKS.md\n"})
	m = updated.(*Model)
	updated, cmd = m.Update(briefProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if cmd == nil || m.job != jobBriefLint || brief.lintTarget != skillDir {
		t.Fatalf("lint lookup: job=%v target=%q", m.job, brief.lintTarget)
	}
	updated, _ = m.Update(briefProcessEvent{Stream: briefexec.Stderr, Text: "brief: 1 skill(s), 0 error(s), 0 warning(s)\n"})
	m = updated.(*Model)
	updated, _ = m.Update(briefProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.running || m.skillLintState != skillLintClean || m.skillBody != strings.TrimSpace(body) || !strings.Contains(m.skillFiles, "CHECKS.md") {
		t.Fatalf("detail state: running=%v lint=%v body=%q files=%q", m.running, m.skillLintState, m.skillBody, m.skillFiles)
	}
}

func TestNewSkillUsesBriefThenPlyWithSourceOnStdin(t *testing.T) {
	workspace := t.TempDir()
	brief := &fakeBrief{newEvents: make(chan briefexec.Event)}
	ply := &fakePly{events: make(chan plyexec.Event)}
	m := New(Config{Workspace: workspace, DataDir: filepath.Join(workspace, ".bench"), Brief: brief, Ply: ply})
	m.screen = screenSkills
	updated, _ := m.Update(key("ctrl+n"))
	m = updated.(*Model)
	m.skillName.SetValue("patch-review")
	m.skillDirectory.SetValue(filepath.Join(workspace, ".claude", "skills"))
	m.skillSource.SetValue("Fixture failures are findings; prose alone is not evidence.")
	updated, cmd := m.Update(key("ctrl+s"))
	m = updated.(*Model)
	if cmd == nil || m.job != jobBriefNew || brief.newRequest.Name != "patch-review" {
		t.Fatalf("brief new: job=%v request=%#v", m.job, brief.newRequest)
	}
	skillDir := filepath.Join(workspace, ".claude", "skills", "patch-review")
	updated, _ = m.Update(briefProcessEvent{Stream: briefexec.Stdout, Text: filepath.Join(skillDir, "SKILL.md") + "\n"})
	m = updated.(*Model)
	updated, cmd = m.Update(briefProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if cmd == nil || m.job != jobPlyRefine || m.screen != screenSkillRun {
		t.Fatalf("ply did not follow scaffold: job=%v screen=%v", m.job, m.screen)
	}
	if ply.request.Dir != skillDir || !strings.Contains(ply.request.Source, "prose alone") || !strings.Contains(ply.request.Goal, "Agent Skill") {
		t.Fatalf("ply request = %#v", ply.request)
	}
	if !strings.HasSuffix(ply.request.SessionDir, filepath.Join("brief", "refine", "patch-review")) {
		t.Fatalf("session dir = %q", ply.request.SessionDir)
	}
	if ply.request.SourceRoot != workspace || !strings.Contains(ply.request.Goal, "$SOURCE_ROOT") {
		t.Fatalf("source root contract = %#v", ply.request)
	}
	updated, _ = m.Update(plyProcessEvent{Stream: plyexec.Stderr, Text: "$ edit SKILL.md\nbrief: clean\n"})
	m = updated.(*Model)
	updated, _ = m.Update(plyProcessEvent{Done: true, ExitCode: 0})
	m = updated.(*Model)
	if m.running || m.skillRunState != skillRunPassed || !strings.Contains(m.skillRunLog, "brief: clean") {
		t.Fatalf("run state=%v running=%v log=%q", m.skillRunState, m.running, m.skillRunLog)
	}
}

func TestBriefLintExitOneIsAnInspectableIssue(t *testing.T) {
	m := New(Config{})
	m.screen = screenSkillDetail
	m.running = true
	m.job = jobBriefLint
	updated, _ := m.Update(briefProcessEvent{Stream: briefexec.Stdout, Text: "SKILL.md:8: warning: body is too long\n"})
	m = updated.(*Model)
	updated, _ = m.Update(briefProcessEvent{Done: true, ExitCode: 1, Err: &fakeExitError{}})
	m = updated.(*Model)
	if m.skillLintState != skillLintIssues || !strings.Contains(m.skillLint, "too long") {
		t.Fatalf("lint state=%v output=%q", m.skillLintState, m.skillLint)
	}
}

func TestSkillCanBeExplicitlyToggledForFutureTasks(t *testing.T) {
	m := New(Config{})
	m.screen = screenSkillDetail
	m.skillDetailName = "go-review"
	updated, _ := m.Update(key("u"))
	m = updated.(*Model)
	if len(m.activeSkills) != 1 || m.activeSkills[0] != "go-review" || !strings.Contains(m.notice, "future task") {
		t.Fatalf("active=%#v notice=%q", m.activeSkills, m.notice)
	}
	updated, _ = m.Update(key("u"))
	m = updated.(*Model)
	if len(m.activeSkills) != 0 {
		t.Fatalf("active after toggle = %#v", m.activeSkills)
	}
}

func TestSkillScreensFitEightyByTwentyFour(t *testing.T) {
	m := New(Config{Workspace: "/work"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.screen = screenSkills
	m.skills = parseSkillCatalogue("go-review\tReview Go patches with executable fixtures.\nweb-perf\tMeasure loading performance.\n")
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)

	m.screen = screenSkillDetail
	m.skillDetailName = "go-review"
	m.skillDetailPath = "/work/.claude/skills/go-review"
	m.skillBody = strings.Repeat("## Step\n\nInspect the patch and run the fixture.\n\n", 20)
	m.skillLintState = skillLintClean
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)

	m.screen = screenSkillForm
	m.skillForm = skillFormNew
	m.skillName.SetValue("go-review")
	m.skillDirectory.SetValue(".claude/skills")
	m.skillSource.SetValue("Long pasted source material")
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)

	m.screen = screenSkillRun
	m.skillRunState = skillRunPassed
	m.skillRunLog = strings.Repeat("$ brief lint -strict .\nclean\n", 30)
	m.syncContent()
	assertTerminalBounds(t, m.View().Content, 80, 24)
}
