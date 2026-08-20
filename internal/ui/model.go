// Package ui implements the bench terminal interface as a Bubble Tea state
// machine. It knows ask only through the process-level askexec.Starter.
package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/honeexec"
	"github.com/patrickyoung/bench/internal/session"
)

const activityLimit = 8192

type role uint8

const (
	roleUser role = iota + 1
	roleAssistant
)

type message struct {
	role role
	text string
}

// Config supplies values bench has already resolved at the command boundary.
type Config struct {
	Runner        askClient
	Draft         draftexec.Client
	Hone          honeexec.Client
	Session       string
	NewSession    string
	Resume        bool
	Choose        bool
	Sessions      []session.Info
	Model         string
	Workspace     string
	Project       string
	InitialPrompt string
}

// Model is the complete ask-screen state.
type Model struct {
	runner     askClient
	draft      draftexec.Client
	hone       honeexec.Client
	session    string
	newSession string
	modelName  string
	workspace  string

	composer        textarea.Model
	project         textinput.Model
	skill           textinput.Model
	viewport        viewport.Model
	messages        []message
	restored        string
	sessions        []session.Info
	selected        int
	picking         bool
	resume          bool
	screen          screen
	formFocus       int
	askComposer     string
	designDir       string
	designBody      string
	designCheck     string
	designBuildable bool
	designBroken    bool
	buildLog        string
	buildAnswer     string
	buildSession    string
	buildState      buildState
	proveLog        string
	proveFindings   string
	proveState      proveState
	learnLog        string
	learnOutput     string
	learnState      learnState

	width       int
	height      int
	dark        bool
	ready       bool
	running     bool
	showHelp    bool
	spinner     int
	stdout      strings.Builder
	activity    string
	notice      string
	cancel      context.CancelFunc
	events      <-chan askexec.Event
	draftEvents <-chan draftexec.Event
	honeEvents  <-chan honeexec.Event
	job         job
}

type processEvent askexec.Event
type tickMsg time.Time
type beginReplayMsg struct{}
type beginProjectMsg struct{}
type draftProcessEvent draftexec.Event
type honeProcessEvent honeexec.Event

type askClient interface {
	askexec.Starter
	askexec.Replayer
}

type job uint8

const (
	jobTurn job = iota + 1
	jobReplay
	jobDraftNew
	jobDraftCheck
	jobDraftBuild
	jobDraftProve
	jobHone
)

type screen uint8

const (
	screenAsk screen = iota
	screenDesignForm
	screenDesignReview
	screenBuild
	screenProve
	screenLearn
)

type buildState uint8

const (
	buildIdle buildState = iota
	buildRunning
	buildPassed
	buildNotDone
	buildFailed
	buildInterrupted
)

type proveState uint8

const (
	proveIdle proveState = iota
	proveRunning
	provePassed
	proveGaps
	proveFailed
	proveInterrupted
)

type learnState uint8

const (
	learnIdle learnState = iota
	learnRunning
	learned
	learnNothing
	learnFailed
	learnInterrupted
)

// New builds an idle workbench without touching the workspace.
func New(cfg Config) Model {
	composer := textarea.New()
	composer.Placeholder = "Describe what you want to build, change, or understand…"
	composer.ShowLineNumbers = false
	composer.Prompt = "│ "
	composer.SetHeight(5)
	composer.SetWidth(72)
	composer.SetValue(cfg.InitialPrompt)
	project := textinput.New()
	project.Placeholder = "agent-name"
	project.Prompt = ""
	project.CharLimit = 240
	project.SetWidth(68)
	skill := textinput.New()
	skill.Placeholder = "agent-house"
	skill.Prompt = ""
	skill.CharLimit = 120
	skill.SetWidth(68)

	view := viewport.New(viewport.WithWidth(76), viewport.WithHeight(12))
	view.SoftWrap = true
	view.MouseWheelEnabled = true

	m := Model{
		runner:     cfg.Runner,
		draft:      cfg.Draft,
		hone:       cfg.Hone,
		session:    cfg.Session,
		newSession: cfg.NewSession,
		modelName:  cfg.Model,
		workspace:  cfg.Workspace,
		composer:   composer,
		project:    project,
		skill:      skill,
		viewport:   view,
		sessions:   cfg.Sessions,
		resume:     cfg.Resume,
		width:      80,
		height:     24,
		dark:       true,
	}
	if m.newSession == "" {
		m.newSession = m.session
	}
	m.picking = cfg.Choose && len(cfg.Sessions) > 0
	if cfg.Project != "" {
		m.designDir = cfg.Project
		m.project.SetValue(filepath.Base(cfg.Project))
		m.screen = screenDesignReview
		m.picking = false
	}
	m.applyTheme()
	m.syncContent()
	return m
}

func (m Model) Init() tea.Cmd {
	if m.designDir != "" && m.screen == screenDesignReview {
		return func() tea.Msg { return beginProjectMsg{} }
	}
	if m.resume {
		return func() tea.Msg { return beginReplayMsg{} }
	}
	if m.picking {
		return nil
	}
	return m.composer.Focus()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height, m.ready = msg.Width, msg.Height, true
		m.resize()
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.applyTheme()
		m.syncContent()
		return m, nil
	case beginReplayMsg:
		return m.startReplay(m.session)
	case beginProjectMsg:
		return m.startDraftCheck()
	case processEvent:
		return m.updateProcess(askexec.Event(msg))
	case draftProcessEvent:
		return m.updateDraftProcess(draftexec.Event(msg))
	case honeProcessEvent:
		return m.updateLearnProcess(honeexec.Event(msg))
	case tickMsg:
		if m.running {
			m.spinner++
			return m, tick()
		}
		return m, nil
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "f1" {
			m.showHelp = !m.showHelp
			if m.showHelp {
				m.composer.Blur()
				m.project.Blur()
			} else if !m.running && !m.picking {
				return m, m.focusCurrent()
			}
			m.syncContent()
			return m, nil
		}
		if m.showHelp {
			if key == "esc" {
				m.showHelp = false
				m.syncContent()
				if !m.running && !m.picking {
					return m, m.focusCurrent()
				}
			}
			return m, nil
		}
		if m.screen == screenDesignForm {
			return m.updateDesignForm(msg, key)
		}
		if m.screen == screenDesignReview {
			return m.updateDesignReview(msg, key)
		}
		if m.screen == screenBuild {
			return m.updateBuild(msg, key)
		}
		if m.screen == screenProve {
			return m.updateProve(msg, key)
		}
		if m.screen == screenLearn {
			return m.updateLearn(msg, key)
		}
		if m.picking {
			return m.updatePicker(key)
		}
		switch key {
		case "ctrl+c":
			if m.running {
				m.interrupt()
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.running {
				m.interrupt()
			}
			return m, nil
		case "ctrl+s":
			if !m.running {
				return m.submit()
			}
			return m, nil
		case "ctrl+d":
			if !m.running {
				return m.openDesign()
			}
			return m, nil
		case "pgup", "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	if m.running || m.picking || m.screen != screenAsk {
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		m.notice = "Write a requirement before sending."
		return m, nil
	}
	if m.runner == nil {
		m.notice = "ask runner is unavailable"
		return m, nil
	}

	m.messages = append(m.messages, message{role: roleUser, text: text})
	m.composer.SetValue("")
	m.composer.Blur()
	m.stdout.Reset()
	m.activity = ""
	m.notice = ""
	m.running = true
	m.spinner = 0
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = m.runner.Start(ctx, askexec.Request{Message: text, Session: m.session})
	m.job = jobTurn
	m.syncContent()
	return m, tea.Batch(waitEvent(m.events), tick())
}

func (m Model) updateProcess(event askexec.Event) (tea.Model, tea.Cmd) {
	if event.Done {
		m.running = false
		m.cancel = nil
		answer := strings.TrimSpace(m.stdout.String())
		if m.job == jobReplay {
			switch {
			case event.Err == nil && event.ExitCode == 0 && answer != "":
				m.restored = answer
				m.messages = nil
				m.picking = false
				m.notice = "Session verified and restored by ask"
			case errors.Is(event.Err, context.Canceled):
				m.notice = "Restore interrupted"
				m.picking = true
			default:
				m.notice = failureNotice(event, m.activity)
				m.picking = true
			}
			m.resume = false
			m.activity = ""
			m.job = 0
			m.syncContent()
			if m.picking {
				return m, nil
			}
			return m, m.composer.Focus()
		}
		switch {
		case event.Err == nil && event.ExitCode == 0 && answer != "":
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
			m.notice = "Turn complete · session is replayable"
		case event.Err == nil && event.ExitCode == 0:
			m.notice = "ask completed without an answer"
		case errors.Is(event.Err, context.Canceled):
			m.notice = "Turn interrupted"
		case event.ExitCode == 2:
			m.notice = "Context full · compact the session before continuing"
		default:
			m.notice = failureNotice(event, m.activity)
		}
		m.activity = ""
		m.job = 0
		m.syncContent()
		return m, m.composer.Focus()
	}

	switch event.Stream {
	case askexec.Stdout:
		m.stdout.WriteString(safeText(event.Text))
	case askexec.Stderr:
		m.appendActivity(event.Text)
	}
	m.syncContent()
	return m, waitEvent(m.events)
}

func (m Model) startReplay(path string) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		m.notice = "ask runner is unavailable"
		return m, nil
	}
	m.session = path
	m.picking = false
	m.running = true
	m.job = jobReplay
	m.stdout.Reset()
	m.activity = "verifying session"
	m.notice = ""
	m.composer.Blur()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = m.runner.Replay(ctx, path)
	m.syncContent()
	return m, tea.Batch(waitEvent(m.events), tick())
}

func (m Model) updatePicker(key string) (tea.Model, tea.Cmd) {
	last := len(m.sessions)
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.selected--
		if m.selected < 0 {
			m.selected = last
		}
	case "down", "j":
		m.selected++
		if m.selected > last {
			m.selected = 0
		}
	case "n":
		return m.startNew()
	case "enter":
		if m.selected == 0 {
			return m.startNew()
		}
		return m.startReplay(m.sessions[m.selected-1].Path)
	}
	m.syncContent()
	return m, nil
}

func (m Model) startNew() (tea.Model, tea.Cmd) {
	m.session = m.newSession
	m.restored = ""
	m.messages = nil
	m.picking = false
	m.notice = "New conversation"
	m.syncContent()
	return m, m.composer.Focus()
}

func (m *Model) interrupt() {
	if m.cancel != nil {
		m.notice = "Interrupting ask…"
		m.cancel()
	}
}

func waitEvent(events <-chan askexec.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return processEvent{Done: true, ExitCode: 1, Err: errors.New("ask event stream closed")}
		}
		return processEvent(event)
	}
}

func tick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func failureNotice(event askexec.Event, activity string) string {
	detail := strings.TrimSpace(activity)
	if lines := strings.Split(detail, "\n"); len(lines) > 0 {
		detail = strings.TrimSpace(lines[len(lines)-1])
	}
	if detail != "" {
		return fmt.Sprintf("ask exited %d · %s", event.ExitCode, detail)
	}
	if event.Err != nil {
		return "ask failed · " + event.Err.Error()
	}
	return fmt.Sprintf("ask exited %d", event.ExitCode)
}

func (m *Model) resize() {
	w := max(24, m.width-4)
	composerHeight := 5
	if m.height < 20 {
		composerHeight = 3
	}
	transcriptHeight := max(3, m.height-composerHeight-7)
	m.composer.SetWidth(w - 2)
	m.composer.SetHeight(composerHeight)
	m.project.SetWidth(w - 6)
	m.skill.SetWidth(w - 6)
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(transcriptHeight)
	m.viewport.YPosition = 2
	m.syncContent()
}

func (m *Model) syncContent() {
	width := max(20, m.viewport.Width())
	var content string
	if m.showHelp {
		content = m.renderHelp(width)
	} else if m.screen == screenDesignForm {
		content = m.renderDesignForm(width)
	} else if m.screen == screenDesignReview {
		content = m.renderDesignReview(width)
	} else if m.screen == screenBuild {
		content = m.renderBuild(width)
	} else if m.screen == screenProve {
		content = m.renderProve(width)
	} else if m.screen == screenLearn {
		content = m.renderLearn(width)
	} else if m.picking {
		content = m.renderPicker(width)
	} else {
		content = m.renderTranscript(width)
	}
	wasBottom := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if m.picking {
		line := 4 + m.selected
		top := m.viewport.YOffset()
		bottom := top + m.viewport.Height() - 1
		switch {
		case line < top:
			m.viewport.SetYOffset(line)
		case line > bottom:
			m.viewport.SetYOffset(line - m.viewport.Height() + 1)
		}
		return
	}
	if m.screen == screenBuild || m.screen == screenProve || m.screen == screenLearn {
		if wasBottom {
			m.viewport.GotoBottom()
		}
		return
	}
	if m.screen != screenAsk {
		return
	}
	if wasBottom || m.running || len(m.messages) <= 2 {
		m.viewport.GotoBottom()
	}
}

func (m Model) renderDesignForm(width int) string {
	t := makeTheme(m.dark)
	projectLabel := t.faint.Render("PROJECT DIRECTORY")
	if m.formFocus == 0 && !m.running {
		projectLabel = t.sessionLabel.Render("PROJECT DIRECTORY")
	}
	rows := []string{
		t.hero.Render("Turn requirements into an agent project."),
		t.muted.Render(ansi.Truncate("draft writes one DESIGN.md: the agreement, procedure, refusals, and definition of done.", max(12, width-4), "…")),
		"",
		projectLabel,
		t.input.Width(max(16, width-6)).Render(m.project.View()),
		"",
		t.faint.Render("Nothing is written until ctrl+s. The project must stay inside this workspace."),
	}
	if m.running {
		rows = append(rows, "", t.working.Render(spinnerFrame(m.spinner)+"  "+lastUsefulLine(m.activity)))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func (m Model) renderDesignReview(width int) string {
	t := makeTheme(m.dark)
	state, stateStyle := m.designVerdict(t)
	heading := t.hero.Render(filepath.Base(m.designDir)) + "  " + stateStyle.Render(state)
	path := t.faint.Render(filepath.Join(m.designDir, "DESIGN.md"))
	body := t.document.Width(max(16, width-5)).Render(m.designBody)
	rows := []string{heading, path, "", body}
	if m.running {
		rows = append(rows, "", t.working.Render(spinnerFrame(m.spinner)+"  "+lastUsefulLine(m.activity)))
	}
	return strings.Join(rows, "\n")
}

func (m Model) designVerdict(t theme) (string, lipgloss.Style) {
	switch {
	case m.running && m.job == jobDraftCheck:
		return "… CHECKING", t.sessionLabel
	case m.designBuildable:
		return "✓ BUILDABLE", t.success
	case m.designBroken:
		return "! CHECK ERROR", t.danger
	default:
		return "○ NEEDS REVISION", t.warning
	}
}

func spinnerFrame(n int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[n%len(frames)]
}

func (m Model) renderTranscript(width int) string {
	t := makeTheme(m.dark)
	if len(m.messages) == 0 && m.restored == "" && !m.running {
		title := t.hero.Render("Start with requirements.")
		body := t.muted.Width(max(20, width-8)).Render(
			"bench keeps the conversation in an append-only ask session. " +
				"Tell it what the agent should do, who it serves, and what would prove it works.")
		examples := t.faint.Render("Try:  Build an agent that reviews Go patches and proves them with go test ./…")
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + body + "\n\n" + examples)
	}
	blocks := make([]string, 0, len(m.messages)+2)
	if m.restored != "" {
		body := t.restoredBlock.Width(max(12, width-5)).Render(m.restored)
		blocks = append(blocks, t.sessionLabel.Render("VERIFIED SESSION")+"\n"+body)
	}
	for _, msg := range m.messages {
		label := t.userLabel.Render("YOU")
		bodyStyle := t.userBlock
		if msg.role == roleAssistant {
			label = t.askLabel.Render("ASK")
			bodyStyle = t.askBlock
		}
		body := bodyStyle.Width(max(12, width-5)).Render(msg.text)
		blocks = append(blocks, label+"\n"+body)
	}
	if m.running {
		line := lastUsefulLine(m.activity)
		if line == "" {
			line = "waiting for ask"
		}
		working := t.working.Render(spinnerFrame(m.spinner) + "  " + line)
		label := t.askLabel.Render("ASK")
		if m.job == jobReplay {
			label = t.sessionLabel.Render("SESSION")
		}
		blocks = append(blocks, label+"\n"+working)
	}
	return strings.Join(blocks, "\n\n")
}

func (m Model) renderPicker(width int) string {
	t := makeTheme(m.dark)
	rows := []string{
		t.hero.Render("Continue a verified conversation."),
		t.muted.Render(ansi.Truncate("bench asks ask to prove a session before rendering or continuing it.", max(10, width-4), "…")),
		"",
		pickerRow(t, m.selected == 0, "New conversation", "blank append-only session", width),
	}
	for i, info := range m.sessions {
		meta := info.Modified.Format("Jan 02 15:04") + "  ·  " + formatBytes(info.Size)
		rows = append(rows, pickerRow(t, m.selected == i+1, info.Name, meta, width))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func pickerRow(t theme, selected bool, name, detail string, width int) string {
	marker := "  "
	style := t.muted
	if selected {
		marker = "› "
		style = t.selected
	}
	nameWidth := max(10, width-27)
	left := style.Width(nameWidth).Render(marker + ansi.Truncate(name, nameWidth-2, "…"))
	right := t.faint.Render(ansi.Truncate(detail, 22, "…"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func (m Model) renderHelp(width int) string {
	t := makeTheme(m.dark)
	if m.screen == screenLearn {
		rows := []string{
			t.hero.Render("Learn keyboard"),
			"",
			helpRow(t, "ctrl+s", "ask hone to admit lessons into the named skill", width),
			helpRow(t, "pgup / pgdown", "scroll hone's provenance", width),
			helpRow(t, "esc", "interrupt, or return to Prove", width),
			helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
			helpRow(t, "f1", "close this help", width),
			"",
			t.muted.Render("hone verifies the build session and admits only lessons from a failed-then-passed recovery."),
		}
		if m.buildSession != "" {
			rows = append(rows, t.code.Width(max(10, width-4)).Render("hone -into "+m.skill.Value()+" "+m.buildSession))
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	if m.screen == screenProve {
		rows := []string{
			t.hero.Render("Prove keyboard"),
			"",
			helpRow(t, "r", "run draft prove again", width),
			helpRow(t, "pgup / pgdown", "scroll mutation results", width),
			helpRow(t, "esc", "interrupt, or return to Build", width),
			helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
			helpRow(t, "f1", "close this help", width),
			"",
			t.muted.Render("Prove mutates the built agent and measures whether its executable check catches each change."),
			t.code.Width(max(10, width-4)).Render("draft prove " + m.designDir),
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	if m.screen == screenBuild {
		rows := []string{
			t.hero.Render("Build keyboard"),
			"",
			helpRow(t, "r", "run draft build again; the worktree is the state", width),
			helpRow(t, "pgup / pgdown", "scroll the typescript", width),
			helpRow(t, "esc", "interrupt, or return to Design", width),
			helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
			helpRow(t, "f1", "close this help", width),
			"",
			t.muted.Render("Done is the DESIGN.md check's exit status, never the model's prose."),
		}
		if m.buildSession != "" {
			rows = append(rows, t.code.Width(max(10, width-4)).Render("ask replay -check "+m.buildSession))
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	if m.screen == screenDesignForm || m.screen == screenDesignReview {
		rows := []string{
			t.hero.Render("Design keyboard"),
			"",
			helpRow(t, "tab", "move between project path and requirements", width),
			helpRow(t, "ctrl+s", "run draft new, then draft check", width),
			helpRow(t, "r", "recheck DESIGN.md from review", width),
			helpRow(t, "pgup / pgdown", "scroll DESIGN.md", width),
			helpRow(t, "esc", "interrupt, or return to Ask", width),
			helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
			helpRow(t, "f1", "close this help", width),
			"",
			t.muted.Render("The document is the project contract. Check it outside bench with:"),
			t.code.Width(max(10, width-4)).Render("draft check " + m.designDir),
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	rows := []string{
		t.hero.Render("Keyboard"),
		"",
		helpRow(t, "ctrl+s", "send requirements", width),
		helpRow(t, "ctrl+d", "turn requirements into a DESIGN.md", width),
		helpRow(t, "enter", "new line in the composer", width),
		helpRow(t, "pgup / pgdown", "scroll the transcript", width),
		helpRow(t, "esc", "interrupt a running ask", width),
		helpRow(t, "f1", "close this help", width),
		helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
		helpRow(t, "↑ / ↓ or j / k", "choose a saved session", width),
		helpRow(t, "n", "start a new conversation", width),
		"",
		t.muted.Render("The durable session is the source of truth. Replay it with:"),
		t.code.Width(max(10, width-4)).Render("ask replay -check " + m.session),
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func helpRow(t theme, key, description string, width int) string {
	left := t.key.Width(18).Render(key)
	right := t.muted.Width(max(10, width-22)).Render(description)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// View uses Bubble Tea v2's declarative terminal capabilities.
func (m Model) View() tea.View {
	t := makeTheme(m.dark)
	w := max(24, m.width)
	state := "ready"
	section := "ask"
	if m.screen == screenDesignForm || m.screen == screenDesignReview {
		section = "design"
	} else if m.screen == screenBuild {
		section = "build"
	} else if m.screen == screenProve {
		section = "prove"
	} else if m.screen == screenLearn {
		section = "learn"
	}
	headerLeft := t.brand.Render("bench") + t.faint.Render("  /  "+section+"  /  "+filepath.Base(m.workspace))
	if m.screen == screenDesignForm && m.running {
		state = "drafting"
	} else if m.screen == screenDesignForm {
		state = "shape"
	} else if m.screen == screenDesignReview && m.running {
		state = "checking"
	} else if m.screen == screenDesignReview && m.designBuildable {
		state = "buildable"
	} else if m.screen == screenDesignReview {
		state = "review"
	} else if m.screen == screenBuild && m.running {
		state = "running"
	} else if m.screen == screenBuild && m.buildState == buildPassed {
		state = "passed"
	} else if m.screen == screenBuild {
		state = "stopped"
	} else if m.screen == screenProve && m.running {
		state = "evaluating"
	} else if m.screen == screenProve && m.proveState == provePassed {
		state = "proven"
	} else if m.screen == screenProve && m.proveState == proveGaps {
		state = "gaps"
	} else if m.screen == screenProve {
		state = "stopped"
	} else if m.screen == screenLearn && m.running {
		state = "learning"
	} else if m.screen == screenLearn && m.learnState == learned {
		state = "learned"
	} else if m.screen == screenLearn && m.learnState == learnNothing {
		state = "nothing"
	} else if m.screen == screenLearn {
		state = "ready"
	} else if m.picking {
		state = "choose"
	} else if m.running && m.job == jobReplay {
		state = "verifying"
	} else if m.running {
		state = "running"
	}
	headerRight := t.state.Render("● " + state)
	gap := max(1, w-lipgloss.Width(headerLeft)-lipgloss.Width(headerRight)-4)
	header := "  " + headerLeft + strings.Repeat(" ", gap) + headerRight + "  "
	header = t.header.Width(w).Render(header)

	composerLabel := t.faint.Render(" REQUIREMENTS ")
	composerContent := m.composer.View()
	if m.screen == screenDesignForm {
		composerLabel = t.faint.Render(" AGENT REQUIREMENTS ")
		if m.formFocus == 1 && !m.running {
			composerLabel = t.sessionLabel.Render(" AGENT REQUIREMENTS ")
		}
	} else if m.screen == screenDesignReview {
		composerLabel = t.faint.Render(" DRAFT CHECK ")
		verdict, verdictStyle := m.designVerdict(t)
		check := m.designCheck
		if check == "" {
			check = "No executable check admitted yet. Edit DESIGN.md, then press r."
		}
		composerContent = verdictStyle.Render(verdict) + "\n" + t.muted.Render(ansi.Truncate(check, max(12, w-10), "…"))
	} else if m.screen == screenBuild {
		composerLabel = t.faint.Render(" BUILD VERDICT ")
		verdict, verdictStyle := m.buildVerdict(t)
		detail := "The DESIGN.md check has not passed."
		if m.buildState == buildPassed {
			detail = "The exact check exited zero. This build is done."
		} else if m.buildState == buildRunning {
			detail = "draft owns the design; ply owns the loop; the check owns done."
		}
		composerContent = verdictStyle.Render(verdict) + "\n" + t.muted.Render(detail)
	} else if m.screen == screenProve {
		composerLabel = t.faint.Render(" EVALUATION VERDICT ")
		verdict, verdictStyle := m.proveVerdict(t)
		detail := "The agent's check has not been evaluated."
		if m.proveState == provePassed {
			detail = "Every generated mutation was detected by the executable check."
		} else if m.proveState == proveGaps {
			detail = "At least one mutation survived. Strengthen the check before trusting it."
		} else if m.proveState == proveRunning {
			detail = "draft is mutating the project and measuring the existing check."
		}
		composerContent = verdictStyle.Render(verdict) + "\n" + t.muted.Render(detail)
	} else if m.screen == screenLearn {
		composerLabel = t.faint.Render(" LEARNING VERDICT ")
		verdict, verdictStyle := m.learnVerdict(t)
		detail := "Name the brief skill that should receive verified lessons."
		if m.learnState == learned {
			detail = "hone admitted a verified recovery into the ordinary skill catalogue."
		} else if m.learnState == learnNothing {
			detail = "The run had no verified recovery worth retaining. Nothing was written."
		} else if m.learnState == learnRunning {
			detail = "hone is verifying evidence before deciding whether anything can be learned."
		}
		composerContent = verdictStyle.Render(verdict) + "\n" + t.muted.Render(detail)
	} else if m.picking {
		composerLabel = t.faint.Render(" SESSION ")
		composerContent = t.muted.Width(max(16, w-10)).Render("↑/↓ choose   enter resume   n new   f1 help")
	}
	composer := t.composer.Width(max(20, w-4)).Height(m.composer.Height()).Render(composerContent)
	notice := m.notice
	if notice == "" {
		if m.screen == screenDesignForm {
			notice = "tab move   ctrl+s draft   esc ask   f1 help"
		} else if m.screen == screenDesignReview {
			notice = "r recheck   pgup scroll   esc ask   f1 help"
		} else if m.screen == screenBuild {
			notice = "r build again   p prove   pgup scroll   esc design   f1 help"
		} else if m.screen == screenProve {
			notice = "r prove again   l learn   pgup scroll   esc build   f1 help"
		} else if m.screen == screenLearn {
			notice = "ctrl+s learn   pgup scroll   esc prove   f1 help"
		} else if m.picking {
			notice = "Nothing opens until ask replay -check succeeds"
		} else {
			notice = "ctrl+s send   enter newline   pgup scroll   f1 help"
		}
	}
	footerLeftText := ansi.Truncate(notice, max(8, w*2/3), "…")
	rightContext := filepath.Base(m.session) + "  ·  " + m.modelName
	if m.screen == screenDesignForm || m.screen == screenDesignReview {
		rightContext = filepath.Base(m.designDir)
		if rightContext == "." || rightContext == "" {
			rightContext = m.project.Value()
		}
		rightContext += "  ·  DESIGN.md"
	} else if m.screen == screenBuild {
		rightContext = filepath.Base(m.designDir) + "  ·  build"
		if m.buildSession != "" {
			rightContext = filepath.Base(m.buildSession)
		}
	} else if m.screen == screenProve {
		rightContext = filepath.Base(m.designDir) + "  ·  prove"
	} else if m.screen == screenLearn {
		rightContext = m.skill.Value() + "  ·  brief skill"
	}
	footerRightText := ansi.Truncate(rightContext, max(8, w/3), "…")
	footerLeft := t.faint.Render(footerLeftText)
	footerRight := t.faint.Render(footerRightText)
	footerGap := max(1, w-lipgloss.Width(footerLeft)-lipgloss.Width(footerRight)-4)
	footer := "  " + footerLeft + strings.Repeat(" ", footerGap) + footerRight + "  "
	if lipgloss.Width(footer) > w {
		footer = "  " + t.faint.Render(ansi.Truncate(notice, max(4, w-4), "…"))
	}

	content := header + "\n" + m.viewport.View() + "\n" + composerLabel + "\n" + composer + "\n" + footer
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "bench · ask"
	if section == "design" {
		view.WindowTitle = "bench · design"
	} else if section == "build" {
		view.WindowTitle = "bench · build"
	} else if section == "prove" {
		view.WindowTitle = "bench · prove"
	} else if section == "learn" {
		view.WindowTitle = "bench · learn"
	}
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

type theme struct {
	header        lipgloss.Style
	brand         lipgloss.Style
	hero          lipgloss.Style
	muted         lipgloss.Style
	faint         lipgloss.Style
	state         lipgloss.Style
	userLabel     lipgloss.Style
	askLabel      lipgloss.Style
	sessionLabel  lipgloss.Style
	userBlock     lipgloss.Style
	askBlock      lipgloss.Style
	restoredBlock lipgloss.Style
	working       lipgloss.Style
	composer      lipgloss.Style
	key           lipgloss.Style
	code          lipgloss.Style
	selected      lipgloss.Style
	input         lipgloss.Style
	document      lipgloss.Style
	success       lipgloss.Style
	warning       lipgloss.Style
	danger        lipgloss.Style
}

func makeTheme(dark bool) theme {
	accent := lipgloss.Color("#7DD3FC")
	secondary := lipgloss.Color("#C4B5FD")
	text := lipgloss.Color("#E5E7EB")
	muted := lipgloss.Color("#94A3B8")
	faint := lipgloss.Color("#64748B")
	border := lipgloss.Color("#334155")
	success := lipgloss.Color("#86EFAC")
	warning := lipgloss.Color("#FCD34D")
	danger := lipgloss.Color("#FDA4AF")
	if !dark {
		accent = lipgloss.Color("#0369A1")
		secondary = lipgloss.Color("#6D28D9")
		text = lipgloss.Color("#172033")
		muted = lipgloss.Color("#526071")
		faint = lipgloss.Color("#718096")
		border = lipgloss.Color("#CBD5E1")
		success = lipgloss.Color("#15803D")
		warning = lipgloss.Color("#A16207")
		danger = lipgloss.Color("#BE123C")
	}
	return theme{
		header:        lipgloss.NewStyle().Bold(true).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border),
		brand:         lipgloss.NewStyle().Bold(true).Foreground(accent),
		hero:          lipgloss.NewStyle().Bold(true).Foreground(text),
		muted:         lipgloss.NewStyle().Foreground(muted),
		faint:         lipgloss.NewStyle().Foreground(faint),
		state:         lipgloss.NewStyle().Foreground(accent),
		userLabel:     lipgloss.NewStyle().Bold(true).Foreground(muted),
		askLabel:      lipgloss.NewStyle().Bold(true).Foreground(secondary),
		sessionLabel:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		userBlock:     lipgloss.NewStyle().Foreground(text).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(border).PaddingLeft(2),
		askBlock:      lipgloss.NewStyle().Foreground(text).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(secondary).PaddingLeft(2),
		restoredBlock: lipgloss.NewStyle().Foreground(muted).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(accent).PaddingLeft(2),
		working:       lipgloss.NewStyle().Foreground(muted).PaddingLeft(2),
		composer:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1),
		key:           lipgloss.NewStyle().Bold(true).Foreground(accent),
		code:          lipgloss.NewStyle().Foreground(text).Background(border).Padding(0, 1),
		selected:      lipgloss.NewStyle().Bold(true).Foreground(text),
		input:         lipgloss.NewStyle().Foreground(text).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).Padding(0, 1),
		document:      lipgloss.NewStyle().Foreground(text),
		success:       lipgloss.NewStyle().Bold(true).Foreground(success),
		warning:       lipgloss.NewStyle().Bold(true).Foreground(warning),
		danger:        lipgloss.NewStyle().Bold(true).Foreground(danger),
	}
}

func (m *Model) applyTheme() {
	t := makeTheme(m.dark)
	styles := m.composer.Styles()
	styles.Focused.Text = t.hero
	styles.Focused.Prompt = t.askLabel
	styles.Focused.Placeholder = t.faint
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Blurred = styles.Focused
	m.composer.SetStyles(styles)
	inputStyles := m.project.Styles()
	inputStyles.Focused.Text = t.hero
	inputStyles.Focused.Prompt = t.askLabel
	inputStyles.Focused.Placeholder = t.faint
	inputStyles.Blurred = inputStyles.Focused
	m.project.SetStyles(inputStyles)
	m.skill.SetStyles(inputStyles)
}

func lastUsefulLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return ansi.TruncateLeft(line, 120, "…")
		}
	}
	return ""
}

var ansiSequence = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)

func safeText(s string) string {
	s = ansiSequence.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			return r
		}
		if r == '\r' {
			return '\n'
		}
		return -1
	}, s)
}

func (m *Model) appendActivity(s string) {
	m.activity += safeText(s)
	if lipgloss.Width(m.activity) > activityLimit {
		m.activity = ansi.TruncateLeft(m.activity, activityLimit, "")
	}
}
