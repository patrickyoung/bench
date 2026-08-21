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
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/honeexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
)

const activityLimit = 8192

type role uint8

const (
	roleUser role = iota + 1
	roleAssistant
	roleTools
)

type message struct {
	role role
	text string
}

// Config supplies values bench has already resolved at the command boundary.
type Config struct {
	Runner        askClient
	Task          plyexec.Worker
	Draft         draftexec.Client
	Hone          honeexec.Client
	Brief         briefexec.Client
	Ply           plyexec.Client
	Session       string
	NewSession    string
	Resume        bool
	Choose        bool
	Sessions      []session.Info
	Model         string
	Workspace     string
	DataDir       string
	Project       string
	InitialPrompt string
	Toolbox       string
	ActiveSkills  []string
	TaskOptions   plyexec.TaskOptions
}

// Model is the pointer-owned state for one Bubble Tea event loop. Bubbles
// widgets carry live cursor state, so the root model must not be copied while
// commands are running.
type Model struct {
	runner       askClient
	task         plyexec.Worker
	draft        draftexec.Client
	hone         honeexec.Client
	brief        briefexec.Client
	ply          plyexec.Client
	session      string
	newSession   string
	modelName    string
	modelDefault string
	workspace    string
	dataDir      string
	toolbox      string
	taskOptions  plyexec.TaskOptions
	taskMode     bool

	composer       textarea.Model
	project        textinput.Model
	skill          textinput.Model
	skillQuery     textinput.Model
	skillName      textinput.Model
	skillDirectory textinput.Model
	// Keep the heavyweight secondary editor indirect so the event-loop object
	// stays lean while it is passed through the tea.Model interface.
	skillSource     *textarea.Model
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
	learnReturn     screen
	skills          []skillEntry
	skillCursor     int
	skillsReturn    screen
	skillForm       skillFormMode
	skillFormFocus  int
	skillDetailName string
	skillDetailPath string
	skillBody       string
	skillFiles      string
	skillLint       string
	skillLintState  skillLintState
	skillRunLog     string
	skillRunAnswer  string
	skillRunState   skillRunState
	skillRunSession string
	activeSkills    []string

	width        int
	height       int
	dark         bool
	ready        bool
	running      bool
	showHelp     bool
	spinner      int
	stdout       strings.Builder
	activity     string
	toolActivity string
	notice       string
	cancel       context.CancelFunc
	events       <-chan askexec.Event
	draftEvents  <-chan draftexec.Event
	honeEvents   <-chan honeexec.Event
	briefEvents  <-chan briefexec.Event
	plyEvents    <-chan plyexec.Event
	job          job
}

type processEvent askexec.Event
type tickMsg time.Time
type beginReplayMsg struct{}
type beginProjectMsg struct{}
type draftProcessEvent draftexec.Event
type honeProcessEvent honeexec.Event
type briefProcessEvent briefexec.Event
type plyProcessEvent plyexec.Event

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
	jobBriefList
	jobBriefPath
	jobBriefCat
	jobBriefFiles
	jobBriefLint
	jobBriefNew
	jobPlyRefine
	jobPlyTask
)

type screen uint8

const (
	screenAsk screen = iota
	screenDesignForm
	screenDesignReview
	screenBuild
	screenProve
	screenLearn
	screenSkills
	screenSkillDetail
	screenSkillForm
	screenSkillRun
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

type skillFormMode uint8

const (
	skillFormNew skillFormMode = iota + 1
	skillFormRefine
)

type skillLintState uint8

const (
	skillLintUnknown skillLintState = iota
	skillLintClean
	skillLintIssues
	skillLintBroken
)

type skillRunState uint8

const (
	skillRunIdle skillRunState = iota
	skillRunRunning
	skillRunPassed
	skillRunNotDone
	skillRunFailed
	skillRunInterrupted
)

// New builds an idle workbench without touching the workspace.
func New(cfg Config) *Model {
	composer := textarea.New()
	composer.Placeholder = "Describe a task, or type /help…"
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
	skillQuery := textinput.New()
	skillQuery.Placeholder = "type to filter by name or description"
	skillQuery.Prompt = "› "
	skillQuery.CharLimit = 240
	skillQuery.SetWidth(68)
	skillName := textinput.New()
	skillName.Placeholder = "patch-review"
	skillName.Prompt = ""
	skillName.CharLimit = 64
	skillName.SetWidth(68)
	skillDirectory := textinput.New()
	skillDirectory.Placeholder = ".claude/skills"
	skillDirectory.Prompt = ""
	skillDirectory.CharLimit = 240
	skillDirectory.SetWidth(68)
	skillSource := textarea.New()
	skillSource.Placeholder = "Paste notes, documentation, logs, examples, feedback, or paths to local source files…"
	skillSource.ShowLineNumbers = false
	skillSource.Prompt = "│ "
	skillSource.SetHeight(5)
	skillSource.SetWidth(72)

	view := viewport.New(viewport.WithWidth(76), viewport.WithHeight(12))
	view.SoftWrap = true
	view.MouseWheelEnabled = true

	m := Model{
		runner:         cfg.Runner,
		task:           cfg.Task,
		draft:          cfg.Draft,
		hone:           cfg.Hone,
		brief:          cfg.Brief,
		ply:            cfg.Ply,
		session:        cfg.Session,
		newSession:     cfg.NewSession,
		modelName:      cfg.Model,
		modelDefault:   cfg.Model,
		workspace:      cfg.Workspace,
		dataDir:        cfg.DataDir,
		toolbox:        cfg.Toolbox,
		taskOptions:    cfg.TaskOptions,
		taskMode:       true,
		composer:       composer,
		project:        project,
		skill:          skill,
		skillQuery:     skillQuery,
		skillName:      skillName,
		skillDirectory: skillDirectory,
		skillSource:    &skillSource,
		viewport:       view,
		sessions:       cfg.Sessions,
		activeSkills:   append([]string(nil), cfg.ActiveSkills...),
		resume:         cfg.Resume,
		width:          80,
		height:         24,
		dark:           true,
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
	return &m
}

func (m *Model) Init() tea.Cmd {
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

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case briefProcessEvent:
		return m.updateBriefProcess(briefexec.Event(msg))
	case plyProcessEvent:
		if m.job == jobPlyTask {
			return m.updateTaskProcess(plyexec.Event(msg))
		}
		return m.updateSkillRunProcess(plyexec.Event(msg))
	case shellReturnedMsg:
		if msg.err != nil {
			m.notice = "Shell exited with an error · " + msg.err.Error()
		} else {
			m.notice = "Returned from operator shell · shell activity is not part of the Ask session"
		}
		m.syncContent()
		return m, m.composer.Focus()
	case editorReturnedMsg:
		if msg.err != nil {
			m.notice = "Editor exited with an error · " + msg.err.Error()
			m.syncContent()
			return m, nil
		}
		m.notice = "Editor closed · rechecking DESIGN.md"
		return m.startDraftCheck()
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
				m.skillQuery.Blur()
				m.skillName.Blur()
				m.skillDirectory.Blur()
				m.skillSource.Blur()
			} else if !m.running && !m.picking {
				cmd := m.focusCurrent()
				return m, cmd
			}
			m.syncContent()
			if m.showHelp {
				m.viewport.GotoTop()
			}
			return m, nil
		}
		if m.showHelp {
			if key == "esc" {
				m.showHelp = false
				m.syncContent()
				if !m.running && !m.picking {
					cmd := m.focusCurrent()
					return m, cmd
				}
			}
			return m, nil
		}
		if key == "ctrl+z" && !m.running && !m.picking {
			m.notice = "Suspended · run fg in the parent shell to return"
			m.syncContent()
			return m, tea.Suspend
		}
		if key == "f2" && !m.running && !m.picking && !isSkillScreen(m.screen) {
			return m.openSkills()
		}
		if isSkillScreen(m.screen) {
			return m.updateSkills(msg, key)
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
		case "ctrl+s", "ctrl+enter":
			if !m.running {
				return m.submit()
			}
			return m, nil
		case "enter":
			if !m.running {
				return m.submit()
			}
			return m, nil
		case "alt+enter", "shift+enter":
			if !m.running {
				m.composer.InsertString("\n")
				m.syncContent()
			}
			return m, nil
		case "ctrl+d":
			if !m.running && strings.TrimSpace(m.composer.Value()) == "" {
				return m, tea.Quit
			}
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

func (m *Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		m.notice = "Describe a task before sending."
		return m, nil
	}
	if strings.HasPrefix(text, "//") {
		text = strings.TrimPrefix(text, "/")
	} else if strings.HasPrefix(text, "/") {
		return m.handleCommand(text)
	}
	if m.taskMode && m.task == nil {
		m.notice = "ply task runner is unavailable"
		return m, nil
	}
	if !m.taskMode && m.runner == nil {
		m.notice = "ask runner is unavailable"
		return m, nil
	}

	m.messages = append(m.messages, message{role: roleUser, text: text})
	m.composer.SetValue("")
	m.composer.Blur()
	m.stdout.Reset()
	m.activity = ""
	m.toolActivity = ""
	m.notice = ""
	m.running = true
	m.spinner = 0
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	if m.taskMode {
		m.plyEvents = m.task.Work(ctx, plyexec.TaskRequest{
			Dir: m.workspace, Goal: text, Session: m.session,
			Skills: append([]string(nil), m.activeSkills...), Toolbox: m.toolbox, Model: m.modelName,
			Options: m.taskOptions,
		})
		m.job = jobPlyTask
		m.syncContent()
		return m, tea.Batch(waitPlyEvent(m.plyEvents), tick())
	}
	m.events = m.runner.Start(ctx, askexec.Request{Message: text, Session: m.session, Skills: append([]string(nil), m.activeSkills...), Model: m.modelName})
	m.job = jobTurn
	m.syncContent()
	return m, tea.Batch(waitEvent(m.events), tick())
}

func (m *Model) updateProcess(event askexec.Event) (tea.Model, tea.Cmd) {
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
			cmd := m.composer.Focus()
			return m, cmd
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
		cmd := m.composer.Focus()
		return m, cmd
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

func (m *Model) updateTaskProcess(event plyexec.Event) (tea.Model, tea.Cmd) {
	if !event.Done {
		switch event.Stream {
		case plyexec.Stdout:
			m.stdout.WriteString(safeText(event.Text))
		case plyexec.Stderr:
			text := safeText(event.Text)
			m.toolActivity = appendVisibleOutput(m.toolActivity, text, activityLimit,
				"[earlier tool activity omitted from this view; the session is authoritative]\n")
			m.appendActivity(text)
		}
		m.syncContent()
		return m, waitPlyEvent(m.plyEvents)
	}

	m.running = false
	m.cancel = nil
	if event.Session != "" {
		m.session = event.Session
	}
	answer := strings.TrimSpace(m.stdout.String())
	toolLog := strings.TrimSpace(m.toolActivity)
	if toolLog != "" {
		m.messages = append(m.messages, message{role: roleTools, text: toolLog})
	}
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.notice = "Task interrupted · the Ask session keeps completed tool evidence"
	case event.Err == nil && event.ExitCode == 0 && m.taskOptions.Check != "":
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		if event.Session == "" {
			m.notice = "Task done · executable check already passed · no model turn"
		} else {
			m.notice = "Task done · executable check passed · session is replayable"
		}
	case event.Err == nil && event.ExitCode == 0:
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		m.notice = "Task stopped · replayable session · no executable check"
	case event.ExitCode == 2:
		m.notice = "Task not done · Ply stopped before completion"
	default:
		m.notice = filterFailure("ply", event.ExitCode, event.Err, toolLog)
	}
	m.activity = ""
	m.toolActivity = ""
	m.job = 0
	m.syncContent()
	cmd := m.composer.Focus()
	return m, cmd
}

func (m *Model) startReplay(path string) (tea.Model, tea.Cmd) {
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

func (m *Model) updatePicker(key string) (tea.Model, tea.Cmd) {
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

func (m *Model) startNew() (tea.Model, tea.Cmd) {
	m.session = m.newSession
	m.restored = ""
	m.messages = nil
	m.picking = false
	m.notice = "New task session"
	m.syncContent()
	cmd := m.composer.Focus()
	return m, cmd
}

func (m *Model) interrupt() {
	if m.cancel != nil {
		name := "process"
		switch m.job {
		case jobTurn, jobReplay:
			name = "ask"
		case jobDraftNew, jobDraftCheck, jobDraftBuild, jobDraftProve:
			name = "draft"
		case jobHone:
			name = "hone"
		case jobBriefList, jobBriefPath, jobBriefCat, jobBriefFiles, jobBriefLint, jobBriefNew:
			name = "brief"
		case jobPlyRefine:
			name = "ply"
		case jobPlyTask:
			name = "task"
		}
		m.notice = "Interrupting " + name + "…"
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
	m.skillQuery.SetWidth(w - 6)
	m.skillName.SetWidth(w - 6)
	m.skillDirectory.SetWidth(w - 6)
	m.skillSource.SetWidth(w - 2)
	m.skillSource.SetHeight(composerHeight)
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
	} else if m.screen == screenSkills {
		content = m.renderSkills(width)
	} else if m.screen == screenSkillDetail {
		content = m.renderSkillDetail(width)
	} else if m.screen == screenSkillForm {
		content = m.renderSkillForm(width)
	} else if m.screen == screenSkillRun {
		content = m.renderSkillRun(width)
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
	if m.screen == screenSkills {
		line := 4 + m.skillCursor
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
	if m.screen == screenBuild || m.screen == screenProve || m.screen == screenLearn || m.screen == screenSkillRun {
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

func (m *Model) renderDesignForm(width int) string {
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
		t.faint.Render("Nothing is written until ctrl+enter (ctrl+s also works). The project stays inside this workspace."),
	}
	if m.running {
		rows = append(rows, "", t.working.Render(spinnerFrame(m.spinner)+"  "+lastUsefulLine(m.activity)))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func (m *Model) renderDesignReview(width int) string {
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

func (m *Model) designVerdict(t theme) (string, lipgloss.Style) {
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

func (m *Model) renderTranscript(width int) string {
	t := makeTheme(m.dark)
	if len(m.messages) == 0 && m.restored == "" && !m.running {
		titleText := "What are we working on?"
		bodyText := "Ask + Ply can inspect and act through ordinary workspace tools. Commands and results stay replayable. Enter runs; /ask disables model-run tools; /agent promotes recurring work."
		exampleText := "Try:  Find why the tests fail and fix the smallest root cause."
		if !m.taskMode {
			titleText = "What should we think through?"
			bodyText = "Ask continues the same replayable session without running commands. Enter sends; /work restores Ask + Ply; /agent promotes your task text into a checked design."
			exampleText = "Try:  Explain the tradeoffs in this design before we change it."
		} else if m.toolbox != "" {
			bodyText = "Ask + Ply can inspect and act through the explicit " + filepath.Base(m.toolbox) + " toolbox. Commands and results stay replayable. Enter runs; /ask disables model-run tools."
		}
		title := t.hero.Render(titleText)
		body := t.muted.Width(max(20, width-8)).Render(bodyText)
		examples := t.faint.Render(exampleText)
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + body + "\n\n" + examples)
	}
	blocks := make([]string, 0, len(m.messages)+2)
	if len(m.activeSkills) > 0 {
		blocks = append(blocks, t.sessionLabel.Render("BRIEF")+"\n"+
			t.restoredBlock.Width(max(12, width-5)).Render("active for future turns  "+strings.Join(m.activeSkills, " · ")))
	}
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
		} else if msg.role == roleTools {
			label = t.sessionLabel.Render("TOOLS")
			bodyStyle = t.restoredBlock
		}
		bodyText := msg.text
		if msg.role == roleTools {
			bodyText += "\n\nTOOLS · END"
		}
		body := bodyStyle.Width(max(12, width-5)).Render(bodyText)
		blocks = append(blocks, label+"\n"+body)
	}
	if m.running {
		line := lastUsefulLine(m.activity)
		if line == "" {
			line = "waiting for ask"
		}
		working := t.working.Render(spinnerFrame(m.spinner) + "  " + line)
		label := t.askLabel.Render("ASK")
		if m.job == jobPlyTask {
			label = t.sessionLabel.Render("TOOLS")
		}
		if m.job == jobReplay {
			label = t.sessionLabel.Render("SESSION")
		}
		blocks = append(blocks, label+"\n"+working)
	}
	return strings.Join(blocks, "\n\n")
}

func (m *Model) renderPicker(width int) string {
	t := makeTheme(m.dark)
	rows := []string{
		t.hero.Render("Continue verified work."),
		t.muted.Render(ansi.Truncate("bench asks Ask to prove a task session before rendering or continuing it.", max(10, width-4), "…")),
		"",
		pickerRow(t, m.selected == 0, "New task", "blank append-only session", width),
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

func (m *Model) renderHelp(width int) string {
	t := makeTheme(m.dark)
	if isSkillScreen(m.screen) {
		rows := []string{t.hero.Render("Skills keyboard"), ""}
		switch m.screen {
		case screenSkills:
			rows = append(rows,
				helpRow(t, "type", "filter brief ls metadata", width),
				helpRow(t, "↑ / ↓", "choose a skill", width),
				helpRow(t, "enter", "inspect raw SKILL.md, files, path, and lint", width),
				helpRow(t, "ctrl+n", "build a project skill from source", width),
				helpRow(t, "ctrl+r", "reload BRIEF_PATH", width))
		case screenSkillDetail:
			rows = append(rows,
				helpRow(t, "u", "toggle this procedure for future task turns", width),
				helpRow(t, "e", "refine from pasted sources or feedback", width),
				helpRow(t, "l", "rerun brief lint -strict", width),
				helpRow(t, "h", "admit a verified build recovery with hone", width),
				helpRow(t, "pgup / pgdown", "scroll SKILL.md", width))
		case screenSkillForm:
			rows = append(rows,
				helpRow(t, "tab", "move through name, destination, and source", width),
				helpRow(t, "ctrl+enter", "create/refine with brief + ply (ctrl+s fallback)", width),
				helpRow(t, "esc", "return without starting a process", width))
		case screenSkillRun:
			rows = append(rows,
				helpRow(t, "enter", "inspect the strict-clean skill", width),
				helpRow(t, "r", "run the same refinement again", width),
				helpRow(t, "pgup / pgdown", "scroll the ply typescript", width))
		}
		rows = append(rows, helpRow(t, "esc", "interrupt, or go back", width), helpRow(t, "f1", "close this help", width), "",
			t.muted.Render("brief owns the files and lint; ply owns edits; hone alone admits verified recoveries."))
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	if m.screen == screenLearn {
		rows := []string{
			t.hero.Render("Learn keyboard"),
			"",
			helpRow(t, "ctrl+enter", "ask hone to admit lessons (ctrl+s fallback)", width),
			helpRow(t, "f2", "browse and refine brief skills", width),
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
			helpRow(t, "f2", "browse and refine brief skills", width),
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
			helpRow(t, "f2", "browse and refine brief skills", width),
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
			helpRow(t, "ctrl+enter", "run draft new/check (ctrl+s fallback)", width),
			helpRow(t, "e", "edit DESIGN.md with $VISUAL or $EDITOR, then recheck", width),
			helpRow(t, "r", "recheck DESIGN.md from review", width),
			helpRow(t, "f2", "browse and refine brief skills", width),
			helpRow(t, "pgup / pgdown", "scroll DESIGN.md", width),
			helpRow(t, "esc", "interrupt, or return to Work", width),
			helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
			helpRow(t, "f1", "close this help", width),
			"",
			t.muted.Render("The document is the project contract. Check it outside bench with:"),
			t.code.Width(max(10, width-4)).Render("draft check " + m.designDir),
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	sendHelp := "run the task with Ask + tools"
	grantHelp := "Tools are Ply programs; without BENCH_TOOLS this is a full shell grant. Replay with:"
	if !m.taskMode {
		sendHelp = "send an Ask-only turn; no commands run"
		grantHelp = "Ask-only and tools mode share one durable session. Replay it with:"
	} else if m.toolbox != "" {
		grantHelp = "Ply can name only programs in " + filepath.Base(m.toolbox) + ". Replay the session with:"
	}
	rows := []string{
		t.hero.Render("Commands & keys"),
		"",
		helpRow(t, "/model SPEC", "show or switch provider/model", width),
		helpRow(t, "/tools MODE", "show; use shell, off, or a toolbox path", width),
		helpRow(t, "/ask · /work", "switch between Ask and Ask + Ply", width),
		helpRow(t, "/skills", "browse and build Brief skills", width),
		helpRow(t, "/agent", "promote user task text into a DESIGN.md", width),
		helpRow(t, "/shell", "open $SHELL; exit returns to Bench", width),
		helpRow(t, "/status", "show mode, model, skills, and work policy", width),
		helpRow(t, "/help · /quit", "show this contract or exit", width),
		helpRow(t, "//text", "send a message beginning with one slash", width),
		"",
		helpRow(t, "enter", sendHelp, width),
		helpRow(t, "alt/shift+enter", "insert a newline", width),
		helpRow(t, "ctrl+s", "run/send alias", width),
		helpRow(t, "f2", "open Skills", width),
		helpRow(t, "pgup / pgdown", "scroll the transcript", width),
		helpRow(t, "esc", "interrupt a running process", width),
		helpRow(t, "f1", "close this help", width),
		helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
		helpRow(t, "ctrl+d", "exit when the prompt is empty", width),
		helpRow(t, "ctrl+z", "suspend; fg returns from the parent shell", width),
		"",
		t.muted.Render(grantHelp),
		t.code.Width(max(10, width-4)).Render("ask replay -check " + m.session),
		"",
		t.muted.Render("Configured work policy:"),
		t.code.Width(max(10, width-4)).Render(m.taskPolicyDisplay()),
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func helpRow(t theme, key, description string, width int) string {
	left := t.key.Width(18).Render(key)
	// Leave one cell unused so the viewport does not soft-wrap an exactly full
	// padded row into a visually blank continuation line at 80 columns.
	right := t.muted.Width(max(10, width-23)).Render(description)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// View uses Bubble Tea v2's declarative terminal capabilities.
func (m *Model) View() tea.View {
	t := makeTheme(m.dark)
	w := max(24, m.width)
	state := "ready"
	section := "ask+ply"
	if !m.taskMode {
		section = "ask"
	}
	if m.screen == screenDesignForm || m.screen == screenDesignReview {
		section = "design"
	} else if m.screen == screenBuild {
		section = "build"
	} else if m.screen == screenProve {
		section = "prove"
	} else if m.screen == screenLearn {
		section = "learn"
	} else if isSkillScreen(m.screen) {
		section = "skills"
	}
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
	} else if isSkillScreen(m.screen) && m.running {
		state = "working"
	} else if m.screen == screenSkillDetail && m.skillLintState == skillLintClean {
		state = "clean"
	} else if m.screen == screenSkillDetail && m.skillLintState == skillLintIssues {
		state = "issues"
	} else if m.screen == screenSkillRun && m.skillRunState == skillRunPassed {
		state = "clean"
	} else if isSkillScreen(m.screen) {
		state = "ready"
	} else if m.picking {
		state = "choose"
	} else if m.running && m.job == jobReplay {
		state = "verifying"
	} else if m.running {
		state = "running"
	}
	modelWidth := min(22, max(4, w/3))
	headerRight := t.state.Render(ansi.Truncate(m.modelDisplay(), modelWidth, "…") + " · ● " + state)
	leftSuffix := "  /  " + section + "  /  " + filepath.Base(m.workspace)
	leftWidth := max(5, w-lipgloss.Width(headerRight)-5)
	headerLeft := t.brand.Render("bench")
	if leftWidth > 5 {
		headerLeft += t.faint.Render(ansi.Truncate(leftSuffix, leftWidth-5, "…"))
	}
	gap := max(1, w-lipgloss.Width(headerLeft)-lipgloss.Width(headerRight)-4)
	header := "  " + headerLeft + strings.Repeat(" ", gap) + headerRight + "  "
	header = t.header.Width(w).Render(header)

	composerLabel := t.warning.Render(" ASK + PLY · FULL SHELL ")
	if m.toolbox != "" {
		composerLabel = t.sessionLabel.Render(" ASK + PLY · TOOLBOX " + filepath.Base(m.toolbox) + " ")
	}
	if !m.taskMode {
		composerLabel = t.askLabel.Render(" ASK · NO MODEL-RUN TOOLS ")
	}
	composerContent := m.composer.View()
	if m.screen == screenAsk && len(m.activeSkills) > 0 {
		mode := "FULL SHELL"
		if m.toolbox != "" {
			mode = "TOOLBOX " + filepath.Base(m.toolbox)
		}
		if !m.taskMode {
			mode = "NO MODEL-RUN TOOLS"
		}
		prefix := "ASK + PLY"
		if !m.taskMode {
			prefix = "ASK"
		}
		composerLabel = t.askLabel.Render(fmt.Sprintf(" %s · %s · %d BRIEF SKILL(S) ", prefix, mode, len(m.activeSkills)))
	}
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
	} else if m.screen == screenSkills {
		composerLabel = t.faint.Render(" SEARCH BRIEF CATALOGUE ")
		composerContent = m.skillQuery.View()
	} else if m.screen == screenSkillForm {
		composerLabel = t.faint.Render(" SOURCE / FEEDBACK ")
		if m.skillFormFocus == 2 && !m.running {
			composerLabel = t.sessionLabel.Render(" SOURCE / FEEDBACK ")
		}
		composerContent = m.skillSource.View()
	} else if m.screen == screenSkillDetail {
		composerLabel = t.faint.Render(" BRIEF LINT -STRICT ")
		verdict, verdictStyle := m.skillLintVerdict(t)
		detail := "The skill has not been checked."
		if m.skillLintState == skillLintClean {
			detail = "The ordinary SKILL.md passes brief's strict executable check."
		} else if m.skillLintState == skillLintIssues {
			detail = "The skill remains readable, but strict lint found work to do."
		}
		composerContent = verdictStyle.Render(verdict) + "\n" + t.muted.Render(detail)
	} else if m.screen == screenSkillRun {
		composerLabel = t.faint.Render(" REFINEMENT VERDICT ")
		verdict, verdictStyle := m.skillRunVerdict(t)
		detail := "Ply is editing the skill; brief lint owns done."
		if m.skillRunState == skillRunPassed {
			detail = "Strict lint exited zero. Inspect the resulting SKILL.md with enter."
		} else if m.skillRunState == skillRunNotDone {
			detail = "Ply stopped before the strict check passed."
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
			notice = "tab move   ctrl+enter draft   esc work   f2 skills   f1 help"
		} else if m.screen == screenDesignReview {
			notice = "e edit   r recheck   pgup scroll   esc work   f2 skills   f1 help"
		} else if m.screen == screenBuild {
			notice = "r build again   p prove   pgup scroll   esc design   f1 help"
		} else if m.screen == screenProve {
			notice = "r prove again   l learn   pgup scroll   esc build   f1 help"
		} else if m.screen == screenLearn {
			notice = "ctrl+enter learn   pgup scroll   esc prove   f2 skills   f1 help"
		} else if m.screen == screenSkills {
			notice = "type filter   ↑/↓ choose   enter inspect   ctrl+n new   esc back"
		} else if m.screen == screenSkillDetail {
			notice = "u use in tasks   e refine   l lint   h verified lesson   esc catalogue"
		} else if m.screen == screenSkillForm {
			notice = "tab move   ctrl+enter run   esc back   f1 help"
		} else if m.screen == screenSkillRun {
			notice = "enter inspect   r run again   pgup scroll   esc back   f1 help"
		} else if m.picking {
			notice = "Nothing opens until ask replay -check succeeds"
		} else {
			notice = "enter run   /model   /tools   /skills   /agent   /help"
		}
	}
	footerLeftText := ansi.Truncate(notice, max(8, w*2/3), "…")
	rightContext := filepath.Base(m.session) + "  ·  " + m.modelDisplay()
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
	} else if m.screen == screenSkills {
		rightContext = fmt.Sprintf("%d skill(s)  ·  BRIEF_PATH", len(m.skills))
	} else if m.screen == screenSkillDetail || m.screen == screenSkillForm {
		rightContext = m.skillDetailName + "  ·  SKILL.md"
		if m.screen == screenSkillForm && m.skillForm == skillFormNew {
			rightContext = m.skillName.Value() + "  ·  new skill"
		}
	} else if m.screen == screenSkillRun {
		rightContext = m.skillDetailName + "  ·  ply"
		if m.skillRunSession != "" {
			rightContext = filepath.Base(m.skillRunSession)
		}
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
	view.WindowTitle = "bench · " + section
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
	m.skillQuery.SetStyles(inputStyles)
	m.skillName.SetStyles(inputStyles)
	m.skillDirectory.SetStyles(inputStyles)
	sourceStyles := m.skillSource.Styles()
	sourceStyles.Focused.Text = t.hero
	sourceStyles.Focused.Prompt = t.askLabel
	sourceStyles.Focused.Placeholder = t.faint
	sourceStyles.Focused.CursorLine = lipgloss.NewStyle()
	sourceStyles.Blurred = sourceStyles.Focused
	m.skillSource.SetStyles(sourceStyles)
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
