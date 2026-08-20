package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/draftexec"
)

func (m *Model) openDesign() (tea.Model, tea.Cmd) {
	if m.designBody != "" {
		m.screen = screenDesignReview
		m.viewport.GotoTop()
		m.composer.Blur()
		m.project.Blur()
		m.syncContent()
		return m, nil
	}
	m.askComposer = m.composer.Value()
	requirements := m.requirementsText()
	m.screen = screenDesignForm
	m.viewport.GotoTop()
	m.formFocus = 0
	m.project.SetValue(projectSlug(requirements))
	m.composer.SetValue(requirements)
	m.composer.Placeholder = "Describe the agent, its users, its refusals, and how to prove it works…"
	m.composer.Blur()
	m.notice = "Review the path and requirements before draft writes anything"
	m.syncContent()
	cmd := m.project.Focus()
	return m, cmd
}

func (m *Model) requirementsText() string {
	var parts []string
	for _, msg := range m.messages {
		if msg.role == roleUser && strings.TrimSpace(msg.text) != "" {
			parts = append(parts, strings.TrimSpace(msg.text))
		}
	}
	if current := strings.TrimSpace(m.composer.Value()); current != "" {
		parts = append(parts, current)
	}
	return strings.Join(parts, "\n\n")
}

func (m *Model) updateDesignForm(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		switch key {
		case "ctrl+c", "esc":
			m.interrupt()
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.backToAsk()
	case "tab", "shift+tab":
		if m.formFocus == 0 {
			m.formFocus = 1
			m.project.Blur()
			m.syncContent()
			cmd := m.composer.Focus()
			return m, cmd
		}
		m.formFocus = 0
		m.composer.Blur()
		m.syncContent()
		cmd := m.project.Focus()
		return m, cmd
	case "ctrl+s":
		return m.startDraftNew()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	if m.formFocus == 0 {
		m.project, cmd = m.project.Update(msg)
	} else {
		m.composer, cmd = m.composer.Update(msg)
	}
	m.syncContent()
	return m, cmd
}

func (m *Model) updateDesignReview(msg tea.Msg, key string) (tea.Model, tea.Cmd) {
	if m.running {
		switch key {
		case "ctrl+c", "esc":
			m.interrupt()
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.backToAsk()
	case "r":
		return m.startDraftCheck()
	case "b":
		if !m.designBuildable {
			m.notice = "Design must pass draft check before build"
			return m, nil
		}
		return m.startBuild()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) backToAsk() (tea.Model, tea.Cmd) {
	m.screen = screenAsk
	m.project.Blur()
	m.composer.Placeholder = "Describe what you want to build, change, or understand…"
	m.composer.SetValue(m.askComposer)
	if m.designBody == "" {
		m.notice = "Design cancelled; nothing was written"
	} else {
		m.notice = "Agent design remains at " + filepath.Join(m.designDir, "DESIGN.md")
	}
	m.syncContent()
	cmd := m.composer.Focus()
	return m, cmd
}

func (m *Model) startDraftNew() (tea.Model, tea.Cmd) {
	if m.draft == nil {
		m.notice = "draft is unavailable"
		return m, nil
	}
	dir, err := ProjectPath(m.workspace, m.project.Value())
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	description := strings.TrimSpace(m.composer.Value())
	if description == "" {
		m.notice = "Write requirements before drafting"
		return m, nil
	}
	m.designDir = dir
	m.designBody = ""
	m.designCheck = ""
	m.designBuildable = false
	m.designBroken = false
	m.running = true
	m.job = jobDraftNew
	m.stdout.Reset()
	m.activity = "starting draft new"
	m.notice = ""
	m.project.Blur()
	m.composer.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.draftEvents = m.draft.New(ctx, draftexec.Request{Dir: dir, Description: description})
	m.syncContent()
	return m, tea.Batch(waitDraftEvent(m.draftEvents), tick())
}

func (m *Model) startDraftCheck() (tea.Model, tea.Cmd) {
	if m.draft == nil {
		m.notice = "draft is unavailable"
		return m, nil
	}
	if m.designDir == "" {
		m.notice = "No agent project to check"
		return m, nil
	}
	m.running = true
	m.job = jobDraftCheck
	m.stdout.Reset()
	m.activity = "checking DESIGN.md"
	m.notice = ""
	m.project.Blur()
	m.composer.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.draftEvents = m.draft.Check(ctx, m.designDir)
	m.syncContent()
	return m, tea.Batch(waitDraftEvent(m.draftEvents), tick())
}

func (m *Model) updateDraftProcess(event draftexec.Event) (tea.Model, tea.Cmd) {
	if m.job == jobDraftBuild {
		return m.updateBuildProcess(event)
	}
	if m.job == jobDraftProve {
		return m.updateProveProcess(event)
	}
	if !event.Done {
		switch event.Stream {
		case draftexec.Stdout:
			m.stdout.WriteString(safeText(event.Text))
		case draftexec.Stderr:
			m.appendActivity(event.Text)
		}
		m.syncContent()
		return m, waitDraftEvent(m.draftEvents)
	}

	m.running = false
	m.cancel = nil
	finishedJob := m.job
	m.job = 0
	if finishedJob == jobDraftNew {
		if event.Err == nil && event.ExitCode == 0 {
			m.activity = ""
			return m.startDraftCheck()
		}
		if errors.Is(event.Err, context.Canceled) {
			m.notice = "Drafting interrupted"
		} else {
			m.notice = filterFailure("draft", event.ExitCode, event.Err, m.activity)
		}
		m.activity = ""
		m.syncContent()
		cmd := m.focusCurrent()
		return m, cmd
	}

	checkOutput := strings.TrimSpace(m.stdout.String())
	m.designCheck = ""
	body, readErr := os.ReadFile(filepath.Join(m.designDir, "DESIGN.md"))
	if readErr == nil {
		m.designBody = safeText(string(body))
		m.screen = screenDesignReview
		m.viewport.GotoTop()
	}
	m.designBuildable = event.Err == nil && event.ExitCode == 0 && readErr == nil
	m.designBroken = event.ExitCode >= 2 || readErr != nil
	switch {
	case readErr != nil:
		m.notice = "draft wrote no readable DESIGN.md · " + readErr.Error()
	case errors.Is(event.Err, context.Canceled):
		m.notice = "Design check interrupted"
	case m.designBuildable:
		m.designCheck = checkOutput
		m.notice = ""
	case event.ExitCode == 1:
		m.notice = "Not buildable yet · edit DESIGN.md, then press r to recheck"
	default:
		m.notice = filterFailure("draft check", event.ExitCode, event.Err, m.activity)
	}
	m.activity = ""
	m.syncContent()
	return m, nil
}

func waitDraftEvent(events <-chan draftexec.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return draftProcessEvent{Done: true, ExitCode: 2, Err: errors.New("draft event stream closed")}
		}
		return draftProcessEvent(event)
	}
}

func (m *Model) focusCurrent() tea.Cmd {
	if m.running || m.picking || m.screen == screenDesignReview || m.screen == screenBuild || m.screen == screenProve {
		return nil
	}
	if m.screen == screenSkills {
		m.composer.Blur()
		m.project.Blur()
		m.skillSource.Blur()
		return m.skillQuery.Focus()
	}
	if m.screen == screenSkillForm {
		m.composer.Blur()
		m.project.Blur()
		switch m.skillFormFocus {
		case 0:
			return m.skillName.Focus()
		case 1:
			return m.skillDirectory.Focus()
		default:
			return m.skillSource.Focus()
		}
	}
	if m.screen == screenSkillDetail || m.screen == screenSkillRun {
		return nil
	}
	if m.screen == screenLearn {
		m.composer.Blur()
		m.project.Blur()
		return m.skill.Focus()
	}
	if m.screen == screenDesignForm && m.formFocus == 0 {
		m.composer.Blur()
		return m.project.Focus()
	}
	m.project.Blur()
	m.skill.Blur()
	return m.composer.Focus()
}

func filterFailure(name string, code int, err error, activity string) string {
	detail := lastUsefulLine(activity)
	if detail != "" {
		return fmt.Sprintf("%s exited %d · %s", name, code, detail)
	}
	if err != nil {
		return name + " failed · " + err.Error()
	}
	return fmt.Sprintf("%s exited %d", name, code)
}

// ProjectPath resolves an agent project while keeping the TUI's file writes
// inside its explicit workspace.
func ProjectPath(workspace, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("project directory is empty")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", errors.New("workspace path is invalid")
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	path = filepath.Clean(path)
	if !pathWithin(workspace, path) {
		return "", errors.New("project directory must stay inside the workspace")
	}
	workspaceReal, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", errors.New("workspace path is unreadable")
	}
	ancestor := path
	for {
		stat, statErr := os.Lstat(ancestor)
		if statErr == nil {
			if ancestor == path && stat.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("project directory may not be a symbolic link")
			}
			real, evalErr := filepath.EvalSymlinks(ancestor)
			if evalErr != nil || !pathWithin(workspaceReal, real) {
				return "", errors.New("project directory must stay inside the workspace")
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", errors.New("project directory is unreadable")
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("project directory has no readable ancestor")
		}
		ancestor = parent
	}
	return path, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func projectSlug(s string) string {
	var out []rune
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			dash = false
		case unicode.IsSpace(r) || r == '-' || r == '_':
			if len(out) > 0 && !dash {
				out = append(out, '-')
				dash = true
			}
		}
		if len(out) >= 36 {
			break
		}
	}
	name := strings.Trim(string(out), "-")
	if name == "" {
		return "agent"
	}
	return name
}
