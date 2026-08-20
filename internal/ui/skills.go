package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
)

const skillOutputLimit = 256 * 1024

type skillEntry struct {
	Name        string
	Description string
}

func isSkillScreen(s screen) bool {
	return s == screenSkills || s == screenSkillDetail || s == screenSkillForm || s == screenSkillRun
}

func (m *Model) openSkills() (tea.Model, tea.Cmd) {
	if m.brief == nil {
		m.notice = "brief is unavailable"
		return m, nil
	}
	m.skillsReturn = m.screen
	m.screen = screenSkills
	m.skillCursor = 0
	m.skillQuery.SetValue("")
	m.composer.Blur()
	m.project.Blur()
	m.skill.Blur()
	return m.startSkillList()
}

func (m *Model) startSkillList() (tea.Model, tea.Cmd) {
	if m.brief == nil {
		m.notice = "brief is unavailable"
		return m, nil
	}
	m.screen = screenSkills
	m.running = true
	m.job = jobBriefList
	m.stdout.Reset()
	m.activity = "reading brief catalogue"
	m.notice = ""
	m.skillQuery.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.briefEvents = m.brief.List(ctx)
	m.syncContent()
	return m, tea.Batch(waitBriefEvent(m.briefEvents), tick())
}

func (m *Model) updateSkills(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenSkills:
		return m.updateSkillCatalogue(msg, key)
	case screenSkillDetail:
		return m.updateSkillDetail(msg, key)
	case screenSkillForm:
		return m.updateSkillForm(msg, key)
	case screenSkillRun:
		return m.updateSkillRun(msg, key)
	default:
		return m, nil
	}
}

func (m *Model) updateSkillCatalogue(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		if key == "ctrl+c" || key == "esc" {
			m.interrupt()
		}
		return m, nil
	}
	visible := m.visibleSkills()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.screen = m.skillsReturn
		m.skillQuery.Blur()
		m.notice = "Skills remain ordinary files on BRIEF_PATH"
		m.syncContent()
		cmd := m.focusCurrent()
		return m, cmd
	case "up", "ctrl+p":
		m.skillCursor--
		if m.skillCursor < 0 {
			m.skillCursor = max(0, len(visible)-1)
		}
		m.syncContent()
		return m, nil
	case "down", "ctrl+j":
		m.skillCursor++
		if m.skillCursor >= len(visible) {
			m.skillCursor = 0
		}
		m.syncContent()
		return m, nil
	case "enter":
		if len(visible) == 0 {
			m.notice = "No matching skill to inspect"
			return m, nil
		}
		return m.startSkillDetail(visible[m.skillCursor].Name)
	case "ctrl+n":
		return m.openNewSkill()
	case "ctrl+r":
		return m.startSkillList()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.skillQuery, cmd = m.skillQuery.Update(msg)
	if m.skillCursor >= len(m.visibleSkills()) {
		m.skillCursor = 0
	}
	m.syncContent()
	return m, cmd
}

func (m *Model) updateSkillDetail(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		switch key {
		case "ctrl+c", "esc":
			m.interrupt()
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.startSkillList()
	case "e":
		return m.openRefineSkill()
	case "u":
		m.toggleActiveSkill(m.skillDetailName)
		m.syncContent()
		return m, nil
	case "l":
		return m.startBriefLint()
	case "h":
		m.skill.SetValue(m.skillDetailName)
		return m.openLearn()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateSkillForm(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		if key == "ctrl+c" || key == "esc" {
			m.interrupt()
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.skillForm == skillFormRefine {
			m.screen = screenSkillDetail
		} else {
			m.screen = screenSkills
		}
		m.skillName.Blur()
		m.skillDirectory.Blur()
		m.skillSource.Blur()
		m.notice = "Skill files were not changed"
		m.syncContent()
		cmd := m.focusCurrent()
		return m, cmd
	case "ctrl+s":
		if m.skillForm == skillFormNew {
			return m.startSkillNew()
		}
		return m.startSkillRefine()
	case "tab", "shift+tab":
		return m.cycleSkillFormFocus(key == "shift+tab")
	}
	var cmd tea.Cmd
	if m.skillForm == skillFormNew && m.skillFormFocus == 0 {
		m.skillName, cmd = m.skillName.Update(msg)
	} else if m.skillForm == skillFormNew && m.skillFormFocus == 1 {
		m.skillDirectory, cmd = m.skillDirectory.Update(msg)
	} else {
		updated := *m.skillSource
		updated, cmd = updated.Update(msg)
		*m.skillSource = updated
	}
	m.syncContent()
	return m, cmd
}

func (m *Model) updateSkillRun(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		switch key {
		case "ctrl+c", "esc":
			m.interrupt()
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.skillDetailPath != "" {
			return m.startSkillDetail(m.skillDetailName)
		}
		return m.startSkillList()
	case "enter":
		if m.skillRunState == skillRunPassed {
			return m.startSkillDetail(m.skillDetailName)
		}
	case "r":
		return m.startSkillRefine()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) openNewSkill() (tea.Model, tea.Cmd) {
	m.screen = screenSkillForm
	m.skillForm = skillFormNew
	m.skillFormFocus = 0
	m.skillName.SetValue("")
	m.skillDirectory.SetValue(filepath.Join(".claude", "skills"))
	m.skillSource.SetValue("")
	m.skillQuery.Blur()
	m.skillDirectory.Blur()
	m.skillSource.Blur()
	m.notice = "brief creates the scaffold; ply turns your source into procedure"
	m.viewport.GotoTop()
	m.syncContent()
	cmd := m.skillName.Focus()
	return m, cmd
}

func (m *Model) openRefineSkill() (tea.Model, tea.Cmd) {
	if m.skillDetailPath == "" {
		m.notice = "brief returned no editable path for this skill"
		return m, nil
	}
	m.screen = screenSkillForm
	m.skillForm = skillFormRefine
	m.skillFormFocus = 2
	m.skillSource.SetValue("")
	m.skillName.Blur()
	m.skillDirectory.Blur()
	m.notice = "Paste sources or feedback; existing instructions and verified lessons stay visible"
	m.viewport.GotoTop()
	m.syncContent()
	cmd := m.skillSource.Focus()
	return m, cmd
}

func (m *Model) cycleSkillFormFocus(reverse bool) (tea.Model, tea.Cmd) {
	if m.skillForm == skillFormRefine {
		m.skillFormFocus = 2
		cmd := m.skillSource.Focus()
		return m, cmd
	}
	step := 1
	if reverse {
		step = -1
	}
	m.skillFormFocus = (m.skillFormFocus + step + 3) % 3
	m.skillName.Blur()
	m.skillDirectory.Blur()
	m.skillSource.Blur()
	m.syncContent()
	switch m.skillFormFocus {
	case 0:
		cmd := m.skillName.Focus()
		return m, cmd
	case 1:
		cmd := m.skillDirectory.Focus()
		return m, cmd
	default:
		cmd := m.skillSource.Focus()
		return m, cmd
	}
}

func (m *Model) startSkillNew() (tea.Model, tea.Cmd) {
	if m.brief == nil {
		m.notice = "brief is unavailable"
		return m, nil
	}
	name := strings.TrimSpace(m.skillName.Value())
	dir := strings.TrimSpace(m.skillDirectory.Value())
	source := strings.TrimSpace(m.skillSource.Value())
	if name == "" || dir == "" || source == "" {
		m.notice = "Name, destination, and source are all required"
		return m, nil
	}
	m.skillDetailName = name
	m.running = true
	m.job = jobBriefNew
	m.stdout.Reset()
	m.activity = "brief new " + name
	m.notice = ""
	m.skillName.Blur()
	m.skillDirectory.Blur()
	m.skillSource.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.briefEvents = m.brief.New(ctx, briefexec.NewRequest{Directory: dir, Name: name})
	m.syncContent()
	return m, tea.Batch(waitBriefEvent(m.briefEvents), tick())
}

func (m *Model) startSkillDetail(name string) (tea.Model, tea.Cmd) {
	if m.brief == nil {
		m.notice = "brief is unavailable"
		return m, nil
	}
	m.screen = screenSkillDetail
	m.skillDetailName = strings.TrimSpace(name)
	m.skillDetailPath = ""
	m.skillBody = ""
	m.skillFiles = ""
	m.skillLint = ""
	m.skillLintState = skillLintUnknown
	m.viewport.GotoTop()
	return m.startBriefJob(jobBriefPath, "resolving skill path", m.brief.Path)
}

func (m *Model) startBriefLint() (tea.Model, tea.Cmd) {
	if m.skillDetailPath == "" {
		m.notice = "No skill path to lint"
		return m, nil
	}
	m.skillLint = ""
	m.skillLintState = skillLintUnknown
	return m.startBriefJob(jobBriefLint, "running brief lint -strict", func(ctx context.Context, _ string) <-chan briefexec.Event {
		return m.brief.Lint(ctx, m.skillDetailPath)
	})
}

type briefStarter func(context.Context, string) <-chan briefexec.Event

func (m *Model) startBriefJob(job job, activity string, start briefStarter) (tea.Model, tea.Cmd) {
	m.running = true
	m.job = job
	m.stdout.Reset()
	m.activity = activity
	m.notice = ""
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.briefEvents = start(ctx, m.skillDetailName)
	m.syncContent()
	return m, tea.Batch(waitBriefEvent(m.briefEvents), tick())
}

func (m *Model) updateBriefProcess(event briefexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case briefexec.Stdout:
			m.stdout.WriteString(safeText(event.Text))
		case briefexec.Stderr:
			m.appendActivity(event.Text)
		}
		m.syncContent()
		return m, waitBriefEvent(m.briefEvents)
	}
	m.running = false
	m.cancel = nil
	finished := m.job
	m.job = 0
	output := strings.TrimSpace(m.stdout.String())
	activity := strings.TrimSpace(m.activity)
	m.activity = ""
	if errors.Is(event.Err, context.Canceled) {
		m.notice = "brief interrupted"
		m.syncContent()
		cmd := m.focusCurrent()
		return m, cmd
	}

	switch finished {
	case jobBriefList:
		if event.ExitCode == 0 && event.Err == nil {
			m.skills = parseSkillCatalogue(output)
			m.notice = fmt.Sprintf("%d skill(s) on BRIEF_PATH", len(m.skills))
		} else if event.ExitCode == 1 {
			m.skills = nil
			m.notice = "No skills on BRIEF_PATH · ctrl+n creates one"
		} else {
			m.notice = filterFailure("brief ls", event.ExitCode, event.Err, activity)
		}
		m.skillCursor = 0
		m.syncContent()
		cmd := m.skillQuery.Focus()
		return m, cmd
	case jobBriefNew:
		if event.ExitCode != 0 || event.Err != nil {
			m.notice = filterFailure("brief new", event.ExitCode, event.Err, activity)
			m.syncContent()
			cmd := m.focusCurrent()
			return m, cmd
		}
		created := firstLine(output)
		if created == "" {
			m.notice = "brief new returned no SKILL.md path"
			m.syncContent()
			cmd := m.focusCurrent()
			return m, cmd
		}
		if !filepath.IsAbs(created) {
			created = filepath.Join(m.workspace, created)
		}
		m.skillDetailPath = filepath.Dir(filepath.Clean(created))
		return m.startSkillRefine()
	case jobBriefPath:
		if event.ExitCode != 0 || event.Err != nil || output == "" {
			m.notice = filterFailure("brief path", event.ExitCode, event.Err, activity)
			m.syncContent()
			return m, nil
		}
		m.skillDetailPath = firstLine(output)
		return m.startBriefJob(jobBriefCat, "reading SKILL.md", func(ctx context.Context, _ string) <-chan briefexec.Event {
			return m.brief.Cat(ctx, filepath.Join(m.skillDetailPath, "SKILL.md"))
		})
	case jobBriefCat:
		if event.ExitCode != 0 || event.Err != nil {
			m.notice = filterFailure("brief cat", event.ExitCode, event.Err, activity)
			m.syncContent()
			return m, nil
		}
		m.skillBody = output
		return m.startBriefJob(jobBriefFiles, "listing bundled files", func(ctx context.Context, _ string) <-chan briefexec.Event {
			return m.brief.Files(ctx, m.skillDetailPath)
		})
	case jobBriefFiles:
		if event.ExitCode == 0 && event.Err == nil {
			m.skillFiles = output
		} else if event.ExitCode != 1 {
			m.notice = filterFailure("brief ls", event.ExitCode, event.Err, activity)
			m.syncContent()
			return m, nil
		}
		return m.startBriefLint()
	case jobBriefLint:
		m.skillLint = strings.TrimSpace(strings.Join(nonempty(output, activity), "\n"))
		switch {
		case event.ExitCode == 0 && event.Err == nil:
			m.skillLintState = skillLintClean
			m.notice = ""
		case event.ExitCode == 1:
			m.skillLintState = skillLintIssues
			m.notice = "Strict lint found issues · e refines from source"
		default:
			m.skillLintState = skillLintBroken
			m.notice = filterFailure("brief lint", event.ExitCode, event.Err, activity)
		}
		m.syncContent()
		return m, nil
	}
	m.syncContent()
	return m, nil
}

func (m *Model) startSkillRefine() (tea.Model, tea.Cmd) {
	if m.ply == nil {
		m.notice = "ply is unavailable"
		return m, nil
	}
	if m.skillDetailPath == "" {
		m.notice = "No skill directory to refine"
		return m, nil
	}
	source := strings.TrimSpace(m.skillSource.Value())
	if source == "" {
		m.notice = "Paste source material or refinement feedback first"
		return m, nil
	}
	goal := "Refine this Agent Skill from the source on stdin. If that source names readable local paths, resolve relative paths from $SOURCE_ROOT and inspect those files as sources too. Preserve correct existing constraints and provenance; make the procedure concrete, progressively disclosed, and concise. Do not claim facts the source does not support. Keep the executable brief lint check passing."
	if m.skillForm == skillFormNew {
		goal = "Turn the source on stdin into a practical Agent Skill in the scaffold. If that source names readable local paths, resolve relative paths from $SOURCE_ROOT and inspect those files as sources too. Extract repeatable procedure, rules, examples, and edge cases without copying irrelevant prose or inventing facts. Keep the executable brief lint check passing."
	}
	sessionRoot := m.dataDir
	if sessionRoot == "" {
		sessionRoot = filepath.Join(m.workspace, ".bench")
	}
	sessionDir := filepath.Join(sessionRoot, "brief", "refine", m.skillDetailName)
	m.screen = screenSkillRun
	m.running = true
	m.job = jobPlyRefine
	m.skillRunState = skillRunRunning
	m.skillRunLog = ""
	m.skillRunAnswer = ""
	m.skillRunSession = ""
	m.notice = ""
	m.skillSource.Blur()
	m.viewport.GotoBottom()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.plyEvents = m.ply.Refine(ctx, plyexec.RefineRequest{
		Dir: m.skillDetailPath, SourceRoot: m.workspace, Goal: goal, Source: source, SessionDir: sessionDir,
	})
	m.syncContent()
	return m, tea.Batch(waitPlyEvent(m.plyEvents), tick())
}

func (m *Model) updateSkillRunProcess(event plyexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case plyexec.Stdout:
			m.skillRunAnswer = appendVisibleOutput(m.skillRunAnswer, event.Text, skillOutputLimit,
				"[earlier ply answer omitted from this view; the session is authoritative]\n")
		case plyexec.Stderr:
			m.skillRunLog = appendVisibleOutput(m.skillRunLog, event.Text, skillOutputLimit,
				"[earlier refinement typescript omitted from this view; the session is authoritative]\n")
		}
		m.syncContent()
		return m, waitPlyEvent(m.plyEvents)
	}
	m.running = false
	m.cancel = nil
	m.job = 0
	sessionRoot := m.dataDir
	if sessionRoot == "" {
		sessionRoot = filepath.Join(m.workspace, ".bench")
	}
	if saved, err := session.Discover(filepath.Join(sessionRoot, "brief", "refine", m.skillDetailName)); err == nil && len(saved) > 0 {
		m.skillRunSession = saved[0].Path
	}
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.skillRunState = skillRunInterrupted
		m.notice = "Refinement interrupted · rerun it against the same files"
	case event.Err == nil && event.ExitCode == 0:
		m.skillRunState = skillRunPassed
		m.notice = ""
	case event.ExitCode == 2:
		m.skillRunState = skillRunNotDone
		m.notice = "Not done · strict lint still fails or a bound was reached"
	default:
		m.skillRunState = skillRunFailed
		m.notice = filterFailure("ply", event.ExitCode, event.Err, m.skillRunLog)
	}
	m.syncContent()
	return m, nil
}

func waitBriefEvent(events <-chan briefexec.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return briefProcessEvent{Done: true, ExitCode: 2, Err: errors.New("brief event stream closed")}
		}
		return briefProcessEvent(event)
	}
}

func waitPlyEvent(events <-chan plyexec.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return plyProcessEvent{Done: true, ExitCode: 1, Err: errors.New("ply event stream closed")}
		}
		return plyProcessEvent(event)
	}
}

func parseSkillCatalogue(output string) []skillEntry {
	var entries []skillEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		name, description, _ := strings.Cut(line, "\t")
		name = strings.TrimSpace(name)
		if name != "" {
			entries = append(entries, skillEntry{Name: name, Description: strings.TrimSpace(description)})
		}
	}
	return entries
}

func (m *Model) visibleSkills() []skillEntry {
	query := strings.ToLower(strings.TrimSpace(m.skillQuery.Value()))
	if query == "" {
		return m.skills
	}
	words := strings.Fields(query)
	var visible []skillEntry
	for _, entry := range m.skills {
		haystack := strings.ToLower(entry.Name + " " + entry.Description)
		match := true
		for _, word := range words {
			if !strings.Contains(haystack, word) {
				match = false
				break
			}
		}
		if match {
			visible = append(visible, entry)
		}
	}
	return visible
}

func (m *Model) renderSkills(width int) string {
	t := makeTheme(m.dark)
	visible := m.visibleSkills()
	rows := []string{
		t.hero.Render("Skills are procedures, not plugins."),
		t.muted.Render(ansi.Truncate("brief lists level-one metadata; instructions stay on disk until you open one.", max(12, width-4), "…")),
		"",
	}
	if m.running {
		rows = append(rows, t.working.Render(spinnerFrame(m.spinner)+"  "+lastUsefulLine(m.activity)))
	} else if len(visible) == 0 {
		rows = append(rows, t.warning.Render("No matching skills."), t.faint.Render("Clear the search, or press ctrl+n to build one from source."))
	} else {
		for i, entry := range visible {
			marker := "  "
			nameStyle := t.muted
			if i == m.skillCursor {
				marker = "› "
				nameStyle = t.selected
			}
			if m.skillIsActive(entry.Name) {
				marker = "● "
				if i == m.skillCursor {
					marker = "◆ "
				}
			}
			nameWidth := min(28, max(14, width/3))
			left := nameStyle.Width(nameWidth).Render(marker + ansi.Truncate(entry.Name, nameWidth-2, "…"))
			right := t.faint.Render(ansi.Truncate(entry.Description, max(10, width-nameWidth-3), "…"))
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
		}
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func (m *Model) renderSkillDetail(width int) string {
	t := makeTheme(m.dark)
	verdict, style := m.skillLintVerdict(t)
	active := ""
	if m.skillIsActive(m.skillDetailName) {
		active = "  " + t.askLabel.Render("● ACTIVE IN ASK")
	}
	rows := []string{
		t.hero.Render(m.skillDetailName) + "  " + style.Render(verdict) + active,
		t.faint.Render(m.skillDetailPath),
		"",
	}
	if m.running {
		rows = append(rows, t.working.Render(spinnerFrame(m.spinner)+"  "+lastUsefulLine(m.activity)))
	}
	if m.skillBody != "" {
		rows = append(rows, t.sessionLabel.Render("SKILL.md"), t.document.Width(max(16, width-5)).Render(m.skillBody))
	}
	if m.skillFiles != "" {
		rows = append(rows, "", t.sessionLabel.Render("BUNDLED FILES"), t.faint.Render(m.skillFiles))
	}
	if m.skillLint != "" {
		rows = append(rows, "", t.sessionLabel.Render("STRICT LINT"), t.document.Width(max(16, width-5)).Render(m.skillLint))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) renderSkillForm(width int) string {
	t := makeTheme(m.dark)
	title := "Refine " + m.skillDetailName
	rows := []string{t.hero.Render(title)}
	if m.skillForm == skillFormNew {
		rows[0] = t.hero.Render("Build a skill from source.")
		nameLabel := t.faint.Render("SKILL NAME")
		dirLabel := t.faint.Render("DESTINATION")
		if m.skillFormFocus == 0 {
			nameLabel = t.sessionLabel.Render("SKILL NAME")
		}
		if m.skillFormFocus == 1 {
			dirLabel = t.sessionLabel.Render("DESTINATION")
		}
		rows = append(rows, "", nameLabel, t.input.Width(max(16, width-6)).Render(m.skillName.View()), "", dirLabel,
			t.input.Width(max(16, width-6)).Render(m.skillDirectory.View()))
	} else {
		rows = append(rows, t.faint.Render(m.skillDetailPath), "",
			t.muted.Render("The current SKILL.md remains authoritative. Ply changes it; strict lint decides done."))
	}
	rows = append(rows, "", t.faint.Render("Paste text, docs, logs, examples, feedback, or paths to local source files below."))
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func (m *Model) renderSkillRun(width int) string {
	t := makeTheme(m.dark)
	verdict, style := m.skillRunVerdict(t)
	rows := []string{
		t.hero.Render(m.skillDetailName) + "  " + style.Render(verdict),
		t.faint.Render("ply -sh -check '\"$BRIEF\" lint -strict .'"),
		"",
	}
	log := strings.TrimSpace(m.skillRunLog)
	if log == "" && m.running {
		log = spinnerFrame(m.spinner) + "  refining skill from stdin"
	}
	if log != "" {
		rows = append(rows, t.sessionLabel.Render("TYPESCRIPT"), t.document.Width(max(16, width-5)).Render(log))
	}
	if answer := strings.TrimSpace(m.skillRunAnswer); answer != "" {
		rows = append(rows, "", t.askLabel.Render("ANSWER"), t.document.Width(max(16, width-5)).Render(answer))
	}
	if m.skillRunSession != "" {
		rows = append(rows, "", t.faint.Render("evidence  "+m.skillRunSession))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) skillLintVerdict(t theme) (string, lipgloss.Style) {
	if m.running {
		return "… READING", t.sessionLabel
	}
	switch m.skillLintState {
	case skillLintClean:
		return "✓ STRICT CLEAN", t.success
	case skillLintIssues:
		return "○ LINT ISSUES", t.warning
	case skillLintBroken:
		return "! LINT BROKEN", t.danger
	default:
		return "○ UNCHECKED", t.muted
	}
}

func (m *Model) skillRunVerdict(t theme) (string, lipgloss.Style) {
	switch m.skillRunState {
	case skillRunRunning:
		return "… REFINING", t.sessionLabel
	case skillRunPassed:
		return "✓ STRICT CLEAN", t.success
	case skillRunNotDone:
		return "○ NOT DONE", t.warning
	case skillRunInterrupted:
		return "○ INTERRUPTED", t.warning
	case skillRunFailed:
		return "! REFINE FAILED", t.danger
	default:
		return "○ NOT STARTED", t.muted
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func nonempty(values ...string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (m *Model) toggleActiveSkill(name string) {
	for i, active := range m.activeSkills {
		if active == name {
			m.activeSkills = append(m.activeSkills[:i], m.activeSkills[i+1:]...)
			m.notice = name + " removed from future Ask turns"
			return
		}
	}
	m.activeSkills = append(m.activeSkills, name)
	m.notice = name + " will shape future Ask turns through brief cat"
}

func (m *Model) skillIsActive(name string) bool {
	for _, active := range m.activeSkills {
		if active == name {
			return true
		}
	}
	return false
}
