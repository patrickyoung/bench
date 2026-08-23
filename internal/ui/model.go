// Package ui implements the bench terminal interface as a Bubble Tea state
// machine. It knows ask only through the process-level askexec.Starter.
package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
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
	"github.com/patrickyoung/bench/internal/autonomy"
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/contractexec"
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
	roleContract
	roleOutcome
	roleStatus
	roleContractDraft
)

type message struct {
	role role
	text string
}

type contractDecision struct {
	Intent    string
	Questions []string
}

// Config supplies values bench has already resolved at the command boundary.
type Config struct {
	Runner          askClient
	ApprovalResults askexec.ApprovalResultReader
	Recorder        askexec.Recorder
	Task            plyexec.Worker
	Contracts       contractexec.Negotiator
	Draft           draftexec.Client
	Hone            honeexec.Client
	Brief           briefexec.Client
	Ply             plyexec.Client
	Session         string
	NewSession      string
	Resume          bool
	Choose          bool
	Sessions        []session.Info
	Model           string
	Workspace       string
	DataDir         string
	Project         string
	InitialPrompt   string
	Toolbox         string
	ActiveSkills    []string
	TaskOptions     plyexec.TaskOptions
	MayPath         string
}

// Model is the pointer-owned state for one Bubble Tea event loop. Bubbles
// widgets carry live cursor state, so the root model must not be copied while
// commands are running.
type Model struct {
	runner           askClient
	approvalResults  askexec.ApprovalResultReader
	recorder         askexec.Recorder
	task             plyexec.Worker
	contracts        contractexec.Negotiator
	draft            draftexec.Client
	hone             honeexec.Client
	brief            briefexec.Client
	ply              plyexec.Client
	session          string
	newSession       string
	subagentsDir     string
	modelName        string
	modelDefault     string
	workspace        string
	dataDir          string
	toolbox          string
	taskOptions      plyexec.TaskOptions
	pendingContract  *plyexec.ContractResult
	pendingApproval  *plyexec.ContractResult
	retryContract    bool
	pendingDecision  *contractDecision
	contractDraft    *contractexec.Draft
	admittedContract *contractexec.Draft
	contractStore    contractexec.FileStore
	contractAudit    bool
	editingContract  bool
	continueContract bool
	steeringPath     string
	activeTaskIntent string
	mayPath          string
	taskMode         bool

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
	buildAdmitted   bool
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

	width          int
	height         int
	dark           bool
	ready          bool
	running        bool
	showHelp       bool
	spinner        int
	stdout         strings.Builder
	activity       string
	toolActivity   string
	notice         string
	cancel         context.CancelFunc
	events         <-chan askexec.Event
	draftEvents    <-chan draftexec.Event
	honeEvents     <-chan honeexec.Event
	briefEvents    <-chan briefexec.Event
	plyEvents      <-chan plyexec.Event
	contractEvents <-chan contractexec.DraftEvent
	job            job
}

type processEvent askexec.Event
type tickMsg time.Time
type beginReplayMsg struct{}
type beginProjectMsg struct{}
type draftProcessEvent draftexec.Event
type honeProcessEvent honeexec.Event
type briefProcessEvent briefexec.Event
type plyProcessEvent plyexec.Event
type contractDraftProcessEvent contractexec.DraftEvent

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
	jobContractDraft
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
	screenContract
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
	composer.Placeholder = "Describe the outcome you want, or type /help…"
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
		runner:          cfg.Runner,
		approvalResults: cfg.ApprovalResults,
		recorder:        cfg.Recorder,
		task:            cfg.Task,
		contracts:       cfg.Contracts,
		draft:           cfg.Draft,
		hone:            cfg.Hone,
		brief:           cfg.Brief,
		ply:             cfg.Ply,
		session:         cfg.Session,
		newSession:      cfg.NewSession,
		subagentsDir:    session.SubagentsDir(cfg.DataDir, cfg.Session),
		contractStore:   contractexec.FileStore{Dir: session.ContractsDir(cfg.DataDir, cfg.Session)},
		modelName:       cfg.Model,
		modelDefault:    cfg.Model,
		workspace:       cfg.Workspace,
		dataDir:         cfg.DataDir,
		toolbox:         cfg.Toolbox,
		taskOptions:     cfg.TaskOptions,
		mayPath:         cfg.MayPath,
		taskMode:        true,
		composer:        composer,
		project:         project,
		skill:           skill,
		skillQuery:      skillQuery,
		skillName:       skillName,
		skillDirectory:  skillDirectory,
		skillSource:     &skillSource,
		viewport:        view,
		sessions:        cfg.Sessions,
		activeSkills:    append([]string(nil), cfg.ActiveSkills...),
		resume:          cfg.Resume,
		width:           80,
		height:          24,
		dark:            true,
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
	case contractDraftProcessEvent:
		return m.updateContractDraftProcess(contractexec.DraftEvent(msg))
	case manualContractMsg:
		return m.updateManualContract(msg)
	case contractAcceptanceMsg:
		return m.updateContractAcceptance(msg)
	case shellReturnedMsg:
		if msg.err != nil {
			m.notice = "Shell exited with an error · " + msg.err.Error()
		} else {
			m.notice = "Returned from operator shell · shell activity is not part of the Ask session"
		}
		m.syncContent()
		return m, m.composer.Focus()
	case approvalReturnedMsg:
		return m.updateApprovalDecision(msg)
	case editorReturnedMsg:
		if msg.err != nil {
			m.editingContract = false
			m.notice = "Editor exited with an error · " + msg.err.Error()
			m.syncContent()
			return m, nil
		}
		if m.editingContract {
			m.editingContract = false
			return m.reloadContractDraft()
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
		if m.screen == screenContract {
			return m.updateContractScreen(msg, key)
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
		case "a":
			if m.pendingApproval != nil && strings.TrimSpace(m.composer.Value()) == "" {
				return m.decidePendingApproval()
			}
		case "esc":
			if m.running {
				m.interrupt()
			}
			return m, nil
		case "ctrl+s", "ctrl+enter":
			if m.canSteerLoop() {
				return m.queueLoopSteering()
			}
			if !m.running {
				return m.submit()
			}
			return m, nil
		case "enter":
			if m.canSteerLoop() {
				return m.queueLoopSteering()
			}
			if !m.running {
				return m.submit()
			}
			return m, nil
		case "alt+enter", "shift+enter":
			if !m.running || m.canSteerLoop() {
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

	if (m.running && !m.canSteerLoop()) || m.picking || m.screen != screenAsk {
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

func (m *Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		m.notice = "Describe the outcome you want before sending."
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
	if m.taskMode && m.taskOptions.Loop && strings.TrimSpace(m.taskOptions.Check) == "" {
		m.notice = "Loop needs an executable verifier · configure /check -- COMMAND before submitting"
		m.syncContent()
		return m, nil
	}
	if m.taskMode && m.taskOptions.IntentContract && m.contracts == nil {
		m.notice = "Contract controller is unavailable · Ply has not started"
		m.syncContent()
		return m, nil
	}
	if m.taskMode && m.taskOptions.IntentContract && m.contractDraft != nil {
		m.notice = "Contract draft pending · /contract reopens it; accept, revise, or start a new session"
		m.syncContent()
		return m, nil
	}
	if m.taskMode && m.pendingContract != nil {
		m.notice = "Review pending · /accept after inspection · /continue to revise · /check -- CMD to strengthen the verifier"
		m.syncContent()
		return m, nil
	}
	if m.taskMode && m.pendingApproval != nil {
		m.notice = "Approval pending · press a or use /approval decide · /contract amends the admitted boundary"
		m.syncContent()
		return m, nil
	}
	if !m.taskMode && m.runner == nil {
		m.notice = "ask runner is unavailable"
		return m, nil
	}
	goal := text
	if m.taskMode && m.pendingDecision != nil {
		goal = resolvedDecisionGoal(*m.pendingDecision, text)
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
		options := m.taskOptions
		m.taskOptions.Force = false
		m.activeTaskIntent = goal
		if options.IntentContract && m.contracts != nil && m.continueContract && m.admittedContract != nil {
			m.continueContract = false
			options.Check = m.admittedContract.Check
			options.CheckAllCriteria = m.admittedContract.CheckAll
			task := m.taskForContract(*m.admittedContract, options)
			if err := plyexec.Validate(task); err != nil {
				cancel()
				m.running = false
				m.cancel = nil
				m.notice = "Contract cannot continue · " + err.Error()
				return m, m.composer.Focus()
			}
			if err := m.armLoopSteering(task.Options); err != nil {
				cancel()
				m.running = false
				m.cancel = nil
				m.notice = "Loop steering is unavailable · " + err.Error()
				return m, m.composer.Focus()
			}
			task.Steering = m.steeringPath
			m.plyEvents = m.contracts.Run(ctx, contractexec.RunRequest{
				Task:  task,
				Draft: *m.admittedContract, Store: m.contractStore, Guidance: text,
			})
			m.job = jobPlyTask
			m.syncContent()
			return m, tea.Batch(waitPlyEvent(m.plyEvents), tick())
		}
		if options.IntentContract && m.contracts != nil {
			m.contractEvents = m.contracts.Compile(ctx, contractexec.DraftRequest{
				Task: plyexec.TaskRequest{
					Dir: m.workspace, Goal: goal, Session: m.session, SubagentsDir: m.subagentsPath(),
					Skills: append([]string(nil), m.activeSkills...), Toolbox: m.toolbox, Model: m.modelName,
					Options: options,
				},
				Store: m.contractStore,
			})
			m.job = jobContractDraft
			m.syncContent()
			return m, tea.Batch(waitContractDraftEvent(m.contractEvents), tick())
		}
		m.plyEvents = m.task.Work(ctx, plyexec.TaskRequest{
			Dir: m.workspace, Goal: goal, Session: m.session, SubagentsDir: m.subagentsPath(),
			Skills: append([]string(nil), m.activeSkills...), Toolbox: m.toolbox, Model: m.modelName,
			Options: options,
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

func resolvedDecisionGoal(decision contractDecision, answer string) string {
	var b strings.Builder
	b.WriteString("Resolve and pursue this previously paused outcome. The decision below answers the compiler's consequential question; it does not replace the original intent.\n\nORIGINAL USER INTENT\n")
	b.WriteString(decision.Intent)
	b.WriteString("\n\nOPEN QUESTIONS\n")
	for _, question := range decision.Questions {
		b.WriteString("- ")
		b.WriteString(question)
		b.WriteByte('\n')
	}
	b.WriteString("\nUSER DECISION\n")
	b.WriteString(answer)
	return b.String()
}

func (m *Model) subagentsPath() string {
	return m.subagentsDir
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
				m.notice = "Session integrity verified and restored by Ask"
				m.restoreContractState()
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
		if event.Contract != "" {
			if loaded, status, err := m.contractStore.Load(); err == nil && status == "admitted" {
				m.admittedContract = &loaded
				m.contractDraft = nil
			}
			m.screen = screenAsk
			if m.canSteerLoop() {
				m.composer.Placeholder = "Steer the running loop at its next model turn…"
				m.composer.Focus()
			} else {
				m.composer.Placeholder = "Describe the outcome you want, or type /help…"
			}
			m.messages = append(m.messages, message{role: roleContract, text: safeText(event.Contract)})
			// Compiler progress belongs to the understanding phase. Start the
			// visible work log cleanly when Ply receives the admitted contract.
			m.toolActivity = ""
			m.activity = ""
			m.notice = "Contract admitted by you · work continues under " + shortDigest(event.ContractDigest)
			m.syncContent()
			return m, waitPlyEvent(m.plyEvents)
		}
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
	m.cleanupLoopSteering()
	m.composer.Placeholder = "Describe the outcome you want, or type /help…"
	if m.taskOptions.IntentContract {
		if loaded, status, err := m.contractStore.Load(); err == nil && status == "admitted" {
			m.admittedContract = &loaded
			m.contractDraft = nil
		}
	}
	if event.Session != "" {
		m.session = event.Session
		m.contractStore = contractexec.FileStore{Dir: session.ContractsDir(m.dataDir, event.Session)}
	}
	answer := strings.TrimSpace(m.stdout.String())
	checked := m.taskOptions.Check != ""
	toolLog := strings.TrimSpace(m.toolActivity)
	if toolLog != "" {
		m.messages = append(m.messages, message{role: roleTools, text: toolLog})
	}
	if event.ContractResult != nil && event.ContractResult.Status != "needs_decision" {
		m.pendingDecision = nil
	}
	if event.ContractResult != nil && event.ContractResult.Status != "review_required" {
		m.pendingContract = nil
	}
	if event.ContractResult != nil && event.ContractResult.Status != "awaiting_approval" {
		m.pendingApproval = nil
	}
	m.retryContract = event.ContractResult != nil && m.taskOptions.Loop &&
		(event.ContractResult.Status == "not_done" || event.ContractResult.Status == "interrupted")
	switch {
	case errors.Is(event.Err, context.Canceled):
		m.notice = "Task interrupted · the Ask session keeps completed tool evidence"
	case event.ContractResult != nil && event.ContractResult.Status == "complete":
		m.pendingContract = nil
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		m.notice = strings.TrimSpace(event.Text)
		if m.notice == "" {
			total := len(event.ContractResult.AdmittedCheckCoverage)
			m.notice = fmt.Sprintf("Outcome complete · operator-admitted check passed %d/%d criteria · session is replayable", total, total)
		}
		m.taskOptions.Check = ""
		m.taskOptions.CheckAllCriteria = false
	case event.ContractResult != nil && event.ContractResult.Status == "review_required":
		pending := *event.ContractResult
		m.pendingContract = &pending
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		m.notice = strings.TrimSpace(event.Text)
		if m.notice == "" {
			total := len(event.ContractResult.Outstanding)
			m.notice = fmt.Sprintf("Ready for review · proposed check coverage %d/%d criteria · %d remain", len(event.ContractResult.ProposedCheckCoverage), total, len(event.ContractResult.Outstanding))
		}
	case event.ContractResult != nil && event.ContractResult.Status == "needs_decision":
		m.pendingContract = nil
		questions := append([]string{}, event.ContractResult.OpenQuestions...)
		for _, approval := range event.ContractResult.PendingApprovals {
			if m.taskOptions.ApprovalPolicy == plyexec.ApprovalEveryAction {
				questions = append(questions, "Pre-work scope decision: "+approval+". May Bench prepare an exact action for this boundary? Exact execution still requires May.")
			} else {
				questions = append(questions, "Permission decision: "+approval+". Approving authorizes this described scope; no execution-time May gate is configured.")
			}
		}
		m.pendingDecision = &contractDecision{
			Intent: m.activeTaskIntent, Questions: questions,
		}
		m.notice = strings.TrimSpace(event.Text)
		if m.notice == "" {
			m.notice = fmt.Sprintf("Needs decision · resolve %d question(s)/approval(s) before work begins", len(questions))
		}
	case event.ContractResult != nil && event.ContractResult.Status == "awaiting_approval":
		pending := *event.ContractResult
		m.pendingApproval = &pending
		m.pendingContract = nil
		m.retryContract = false
		m.notice = strings.TrimSpace(event.Text)
		if m.notice == "" && pending.ApprovalReceipt != nil {
			m.notice = "Approval required · action " + pending.ApprovalReceipt.Digest + " was not executed"
		}
	case event.ContractResult != nil && event.ContractResult.Status == "approval_declined":
		m.pendingApproval = nil
		m.pendingContract = nil
		m.retryContract = m.admittedContract != nil
		m.notice = strings.TrimSpace(event.Text)
		if m.notice == "" {
			m.notice = "Approval declined · the action was not executed · /continue can pursue another approach"
		}
	case m.taskOptions.IntentContract && event.ContractResult == nil:
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		m.notice = "Task not accepted · contracted work ended without a sealed contract result"
		if event.Err != nil {
			m.notice += " · " + event.Err.Error()
		}
		m.notice += " · check retained"
	case event.Err == nil && event.ExitCode == 0 && checked:
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		if event.Session == "" {
			m.notice = "Task done · executable check already passed · no model turn"
		} else {
			m.notice = "Task done · executable check passed · session is replayable"
		}
		m.taskOptions.Check = ""
		m.notice += " · check cleared for next outcome"
	case event.Err == nil && event.ExitCode == 0:
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		m.notice = "Task stopped · replayable session · no executable check"
	case event.ExitCode == 2:
		if answer != "" {
			m.messages = append(m.messages, message{role: roleAssistant, text: answer})
		}
		m.notice = "Task not done · Ply stopped before completion"
	default:
		m.notice = filterFailure("ply", event.ExitCode, event.Err, toolLog)
	}
	// Keep the verdict after the answer so it is the last durable card and the
	// next interaction stays visible without scrolling backward.
	if event.ContractResult != nil {
		m.messages = append(m.messages, message{role: roleOutcome, text: safeText(contractResultCard(*event.ContractResult))})
	}
	m.activity = ""
	m.toolActivity = ""
	m.job = 0
	m.syncContent()
	cmd := m.composer.Focus()
	return m, cmd
}

func contractResultCard(result plyexec.ContractResult) string {
	lines := []string{}
	if result.Pursuit != "" {
		lines = append(lines, fmt.Sprintf("LOOP · this invocation · cycles=%s · turns=%s · stop=%s", result.CycleBudget, result.TurnBudget, result.StopReason))
	}
	switch result.Status {
	case "complete":
		lines = append(lines, "COMPLETE", fmt.Sprintf("Operator-admitted check settled %d criterion/criteria.", len(result.AdmittedCheckCoverage)))
		if result.VerifierReceipt != nil {
			lines = append(lines, "Evidence: terminal Ply verifier receipt · "+shortDigest(result.VerifierReceipt.BodySHA256))
		}
		lines = append(lines, "Next: describe the next outcome when you are ready.")
	case "review_required":
		lines = append(lines, "READY FOR REVIEW")
		if result.CheckConfigured && result.CheckPassed {
			lines = append(lines, "Configured check passed, but it was not admitted as the whole verdict.")
		} else if result.CheckConfigured {
			lines = append(lines, "A configured check exists; the outcome still needs judgment.")
		} else {
			lines = append(lines, "No executable check was configured for the whole outcome.")
		}
		if len(result.Outstanding) > 0 {
			lines = append(lines, "Still needs acceptance:")
			for _, criterion := range result.Outstanding {
				lines = append(lines, fmt.Sprintf("- %s · %s", criterion.ID, criterion.Judge))
			}
		}
		lines = append(lines, "Next: inspect the artifacts, then /accept · or /continue to revise.")
	case "needs_decision":
		lines = append(lines, "DECISION NEEDED", "Bench paused before workspace tools ran.")
		for _, question := range result.OpenQuestions {
			lines = append(lines, "- "+question)
		}
		for _, approval := range result.PendingApprovals {
			if result.ApprovalPolicy == plyexec.ApprovalEveryAction {
				lines = append(lines, "- Pre-work scope decision; exact execution still requires May: "+approval)
			} else {
				lines = append(lines, "- Permission decision; approval authorizes this scope because no May gate is configured: "+approval)
			}
		}
		lines = append(lines, "Next: reply normally with the missing decision.")
	case "awaiting_approval":
		lines = append(lines, "APPROVAL REQUIRED", "The proposed action was not executed.")
		if result.ApprovalReceipt != nil {
			lines = append(lines, "May digest: "+result.ApprovalReceipt.Digest, "Exact action envelope:", result.ApprovalReceipt.Action)
		}
		lines = append(lines, "Next: press a or /approval decide to hand the exact digest to May.")
	case "approval_declined":
		lines = append(lines, "APPROVAL DECLINED", "The proposed action was not executed.", "Next: /continue can pursue a different action, or /contract can amend the outcome.")
	case "not_done":
		lines = append(lines, "NOT DONE", "Ply stopped before the outcome check accepted the work.", "Next: review the work log, then /continue under this admission · or /contract to amend it.")
	case "interrupted":
		lines = append(lines, "INTERRUPTED", "Completed observations remain in the replayable session.", "Next: /continue under this admission when you are ready.")
	default:
		lines = append(lines, "NOT ACCEPTED", "Bench could not establish a trustworthy outcome verdict.", "Next: inspect the work log and error; the check remains available for retry.")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) startReplay(path string) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		m.notice = "ask runner is unavailable"
		return m, nil
	}
	m.session = path
	m.subagentsDir = session.SubagentsDir(m.dataDir, path)
	m.contractStore = contractexec.FileStore{Dir: session.ContractsDir(m.dataDir, path)}
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
	m.subagentsDir = session.SubagentsDir(m.dataDir, m.newSession)
	m.contractStore = contractexec.FileStore{Dir: session.ContractsDir(m.dataDir, m.newSession)}
	m.pendingContract = nil
	m.retryContract = false
	m.pendingDecision = nil
	m.contractDraft = nil
	m.admittedContract = nil
	m.activeTaskIntent = ""
	m.taskOptions.Force = false
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
		case jobContractDraft:
			name = "contract compiler"
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
	} else if m.screen == screenContract {
		content = m.renderContract(width)
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
		titleText := "What outcome should we pursue?"
		bodyText := "1  NEGOTIATE   Review, revise, or edit a durable contract draft.\n2  ADMIT       You accept the exact contract before Ply starts.\n3  WORK        Use ordinary programs and show their real output.\n4  VERIFY      Let evidence decide: complete, review, or revise."
		exampleText := "Try: Find why the tests fail and fix the smallest root cause.\nHave a check? /check -- COMMAND  ·  f1 shows the whole map."
		if !m.taskMode {
			titleText = "What should we think through?"
			bodyText = "Ask continues the same replayable session without running commands.\nNo workspace program runs until you return with /work."
			exampleText = "Try: Explain the tradeoffs in this design before we change it.\nNeed durable work? /work  ·  Need an agent project? /agent"
		} else if m.autonomyMode() == autonomy.Quick {
			bodyText = "1  WORK        Start Ply immediately with ordinary programs.\n2  OBSERVE     Show real command output as it happens.\n3  VERIFY      Let executable evidence or your review decide what remains."
			exampleText = "Autonomy quick · switch with /mode review when you want a negotiated contract.\nTry: Find why the tests fail and fix the smallest root cause."
		} else if m.autonomyMode() == autonomy.Loop {
			bodyText = "1  NEGOTIATE   Review and admit one durable outcome.\n2  LOOP        Ply keeps trying in this invocation until the check accepts or a bound stops it.\n3  STEER       Queue guidance between model turns while tools and verifier stay fixed."
			exampleText = "Loop needs /check -- COMMAND · /check all separately declares it sufficient for every criterion."
		} else if m.toolbox != "" {
			bodyText = "1  NEGOTIATE   Review, revise, or edit a durable contract draft.\n2  ADMIT       You accept the exact contract before Ply starts.\n3  WORK        Use the " + filepath.Base(m.toolbox) + " toolbox and show real output.\n4  VERIFY      Let evidence decide: complete, review, or revise."
		}
		title := t.hero.Render(titleText)
		body := t.muted.Width(max(20, width-8)).Render(bodyText)
		examples := t.faint.Render(exampleText)
		return lipgloss.NewStyle().Padding(1, 2).Render(title + "\n\n" + body + "\n\n" + examples)
	}
	blocks := make([]string, 0, len(m.messages)+2)
	if len(m.activeSkills) > 0 {
		blocks = append(blocks, t.sessionLabel.Render("BRIEF")+"\n"+
			t.restoredBlock.Width(max(12, width-5)).Render("Shapes understanding and work; never the verdict.\n"+safeText(strings.Join(m.activeSkills, " · "))))
	}
	if m.restored != "" {
		body := t.restoredBlock.Width(max(12, width-5)).Render(m.restored)
		blocks = append(blocks, t.sessionLabel.Render("INTEGRITY-VERIFIED SESSION")+"\n"+body)
	}
	for _, msg := range m.messages {
		label := t.userLabel.Render("YOU")
		bodyStyle := t.userBlock
		if msg.role == roleAssistant {
			label = t.askLabel.Render("ASK")
			bodyStyle = t.askBlock
		} else if msg.role == roleTools {
			label = t.sessionLabel.Render("WORK LOG")
			bodyStyle = t.restoredBlock
		} else if msg.role == roleContract {
			label = t.sessionLabel.Render("CONTRACT")
			bodyStyle = t.restoredBlock
		} else if msg.role == roleOutcome {
			label = t.sessionLabel.Render("OUTCOME")
			bodyStyle = t.restoredBlock
		} else if msg.role == roleStatus {
			label = t.sessionLabel.Render("STATUS")
			bodyStyle = t.restoredBlock
		} else if msg.role == roleContractDraft {
			label = t.sessionLabel.Render("CONTRACT DRAFT")
			bodyStyle = t.restoredBlock
		}
		bodyText := msg.text
		if msg.role == roleTools {
			bodyText += "\n\nWORK LOG · END"
		}
		body := bodyStyle.Width(max(12, width-5)).Render(bodyText)
		blocks = append(blocks, label+"\n"+body)
	}
	if m.running {
		line := lastUsefulLine(m.activity)
		if line == "" {
			line = "waiting for Ask"
		}
		workingLine := spinnerFrame(m.spinner) + "  " + line
		working := t.working.Render(workingLine)
		label := t.askLabel.Render("ASK")
		if m.job == jobPlyTask {
			label = t.sessionLabel.Render("WORKING · LIVE")
			phase := "Understanding the outcome · no workspace command has run yet."
			if m.currentTaskHasContract() {
				phase = "Using workspace tools · commands and observations appear below."
			}
			live := tailLines(strings.TrimSpace(m.toolActivity), 7)
			if live == "" {
				live = phase + "\n\n" + workingLine
			} else {
				live = phase + "\n\n" + live + "\n\n" + workingLine
			}
			working = t.restoredBlock.Width(max(12, width-5)).Render(live)
		}
		if m.job == jobReplay {
			label = t.sessionLabel.Render("SESSION")
		}
		blocks = append(blocks, label+"\n"+working)
	}
	return strings.Join(blocks, "\n\n")
}

func (m *Model) currentTaskHasContract() bool {
	for i := len(m.messages) - 1; i >= 0; i-- {
		switch m.messages[i].role {
		case roleContract:
			return true
		case roleUser:
			return false
		}
	}
	return false
}

func tailLines(text string, count int) string {
	if text == "" || count <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > count {
		lines = append([]string{"… earlier work remains in the replayable session"}, lines[len(lines)-count:]...)
	}
	return strings.Join(lines, "\n")
}

func shortDigest(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
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
	if m.screen == screenContract {
		rows := []string{
			t.hero.Render("Contract review keyboard"), "",
			helpRow(t, "type + enter", "revise the proposal without starting Ply", width),
			helpRow(t, "e", "edit the durable JSON working copy", width),
			helpRow(t, "a", "toggle exact IDs, digests, and file details", width),
			helpRow(t, "ctrl+s", "admit the exact reviewed contract and begin work", width),
			helpRow(t, "esc", "return to Work while retaining the draft", width),
			helpRow(t, "f1", "close this help", width), "",
			t.muted.Render("Drafts are mutable proposals. Admitted revisions and their evidence are immutable."),
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
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
			helpRow(t, "b / B", "ordinary build / admitted build (run draft admit first)", width),
			helpRow(t, "f2", "browse and refine brief skills", width),
			helpRow(t, "pgup / pgdown", "scroll DESIGN.md", width),
			helpRow(t, "esc", "interrupt, or return to Work", width),
			helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
			helpRow(t, "f1", "close this help", width),
			"",
			t.muted.Render("The document is the project contract. Check it outside bench with:"),
			t.code.Width(max(10, width-4)).Render("draft check " + m.designDir + "\ndraft admit " + m.designDir),
		}
		return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))
	}
	sendHelp := "draft an outcome contract; Ply waits for your admission"
	grantHelp := "Tools are Ply programs; without BENCH_TOOLS this is a full shell grant. Replay with:"
	if !m.taskMode {
		sendHelp = "send an Ask-only turn; no commands run"
		grantHelp = "Ask-only and tools mode share one durable session. Replay it with:"
	} else if m.toolbox != "" {
		grantHelp = "Ply can name only programs in " + filepath.Base(m.toolbox) + ". Replay the session with:"
	}
	if m.taskMode && m.autonomyMode() == autonomy.Quick {
		sendHelp = "start Ply immediately without contract review"
	} else if m.taskMode && m.autonomyMode() == autonomy.Loop {
		sendHelp = "draft once, then run one bounded and steerable Ply verifier loop"
	}
	rows := []string{
		t.hero.Render("Working with Bench"),
		t.muted.Render("Outcome → editable proposal → your admission → real work → evidence or review."),
		"",
		t.sessionLabel.Render("OUTCOME & EVIDENCE"),
		helpRow(t, "enter", sendHelp, width),
		helpRow(t, "/check -- CMD", "attach one literal verifier to the next outcome", width),
		helpRow(t, "/check all", "you declare that check sufficient for every criterion", width),
		helpRow(t, "/approval every-action", "require an exact May decision before each model action", width),
		helpRow(t, "/accept", "accept remaining criteria after inspecting the result", width),
		helpRow(t, "/continue", "retry work under the same admitted contract", width),
		helpRow(t, "/mode quick|review|loop", "choose immediate, negotiated, or verifier-loop work", width),
		helpRow(t, "/contract", "reopen the durable draft or admitted revision", width),
		helpRow(t, "/contract edit|import", "edit JSON here, or seal external edits", width),
		helpRow(t, "/contract accept", "admit reviewed bytes, then start Ply", width),
		helpRow(t, "/contract run", "retry the exact admitted revision", width),
		helpRow(t, "/contract on|off", "compatibility aliases for review/quick", width),
		"",
		t.sessionLabel.Render("PARTNER & CAPABILITIES"),
		helpRow(t, "/model SPEC", "show or switch provider/model", width),
		helpRow(t, "/tools MODE", "show; use shell, off, or a toolbox path", width),
		helpRow(t, "/ask · /work", "switch between Ask and Ask + Ply", width),
		helpRow(t, "/skills", "procedures that shape understanding and work", width),
		helpRow(t, "/agent", "promote recurring work into DESIGN.md + durable check", width),
		helpRow(t, "/shell", "use your own $SHELL; activity is outside the session", width),
		helpRow(t, "/status", "add the exact authority and evidence paths to transcript", width),
		helpRow(t, "subagents", "ask for up to 3 read-heavy jobs; root synthesizes", width),
		"",
		t.sessionLabel.Render("KEYBOARD"),
		helpRow(t, "alt/shift+enter", "insert a newline", width),
		helpRow(t, "ctrl+s", "run/send alias", width),
		helpRow(t, "f2", "open Skills", width),
		helpRow(t, "pgup / pgdown", "scroll the transcript", width),
		helpRow(t, "esc", "interrupt a running process", width),
		helpRow(t, "f1", "close this help", width),
		helpRow(t, "ctrl+c", "interrupt; press again when idle to quit", width),
		helpRow(t, "ctrl+d", "exit when the prompt is empty", width),
		helpRow(t, "ctrl+z", "suspend; fg returns from the parent shell", width),
		helpRow(t, "/help · /quit", "show this map or exit", width),
		helpRow(t, "//text", "send a message beginning with one slash", width),
		"",
		t.muted.Render(grantHelp),
		t.code.Width(max(10, width-4)).Render("ask replay -check " + m.session),
		"",
		t.muted.Render("Configured work policy:"),
		t.code.Width(max(10, width-4)).Render(m.taskPolicyDisplay()),
		"",
		t.muted.Render("Fresh subagent sessions (created only when delegated):"),
		t.code.Width(max(10, width-4)).Render(m.subagentsPath()),
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
	} else if m.screen == screenContract {
		section = "contract"
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
	} else if m.screen == screenContract && m.running {
		state = "revising"
	} else if m.screen == screenContract && m.contractDraft != nil {
		state = "draft"
	} else if m.screen == screenContract {
		state = "admitted"
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
	if m.screen == screenAsk && m.taskMode {
		policy := " · " + strings.ToUpper(string(m.autonomyMode())) + " · NO CHECK · /check -- COMMAND "
		if m.taskOptions.Check != "" {
			policy = " · " + strings.ToUpper(string(m.autonomyMode())) + " · CHECK " + strconv.Quote(m.taskOptions.Check) + " "
			if m.taskOptions.CheckAllCriteria {
				policy = " · " + strings.ToUpper(string(m.autonomyMode())) + " · CHECK ALL " + strconv.Quote(m.taskOptions.Check) + " "
			}
		}
		composerLabel += t.warning.Render(ansi.Truncate(policy, max(12, w-lipgloss.Width(composerLabel)-2), "…"))
	}
	if m.screen == screenContract {
		composerLabel = t.sessionLabel.Render(" CONTRACT REVISION ")
		if m.running {
			composerContent = t.muted.Render("The read-only compiler is revising the proposal. Ply has not started.")
		}
	} else if m.screen == screenDesignForm {
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
			if m.buildAdmitted {
				detail = "The admitted verifier exited zero outside the worker's write grant."
			}
		} else if m.buildState == buildRunning {
			detail = "draft owns the design; ply owns the loop; the check owns done."
			if m.buildAdmitted {
				detail = "Cage protects the May-approved verifier while Ply works."
			}
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
		if m.screen == screenContract {
			notice = "type revise   e edit JSON   a audit   ctrl+s accept/run   esc keep   f1 help"
		} else if m.screen == screenDesignForm {
			notice = "tab move   ctrl+enter draft   esc work   f2 skills   f1 help"
		} else if m.screen == screenDesignReview {
			notice = "e edit   r recheck   b build   B admitted   pgup scroll   esc work"
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
			notice = "Nothing opens until ask replay -check verifies session integrity"
		} else {
			notice = m.defaultWorkHint()
		}
	}
	footerLeftText := ansi.Truncate(notice, max(8, w*2/3), "…")
	rightContext := filepath.Base(m.session) + "  ·  " + m.modelDisplay()
	if m.screen == screenContract {
		rightContext = filepath.Base(m.contractStore.DraftPath())
	} else if m.screen == screenDesignForm || m.screen == screenDesignReview {
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

func (m *Model) defaultWorkHint() string {
	if !m.taskMode {
		return "enter ask   /work enables tools   /agent promotes durable work   f1 map"
	}
	if m.retryContract {
		return "loop stopped   /continue retries the admitted outcome   /contract amends it"
	}
	if m.pendingApproval != nil {
		return "approval required   a decide with May   /continue changes approach   /contract amends"
	}
	if m.autonomyMode() == autonomy.Quick && m.contractDraft == nil && m.pendingContract == nil {
		return "autonomy quick   enter starts Ply without contract review   /mode review negotiates first"
	}
	if m.autonomyMode() == autonomy.Loop && m.contractDraft == nil && m.pendingContract == nil {
		return "autonomy loop   /check required   one invocation · finite turns · live steering"
	}
	if m.contractDraft != nil {
		return "contract draft retained   /contract review   /contract accept runs"
	}
	if m.pendingDecision != nil {
		return "decision pending   reply normally with the missing answer or approval"
	}
	if m.pendingContract != nil {
		return "review pending   /accept after inspection   /continue retries same contract"
	}
	if m.taskOptions.CheckAllCriteria {
		return "describe outcome   check will judge every criterion after contract admission"
	}
	if m.taskOptions.Check != "" {
		return "describe outcome   check is bound into the draft   /check all changes authority"
	}
	return "describe an outcome   /check adds evidence   /skills adds procedure   f1 shows the map"
}

func (m *Model) autonomyMode() autonomy.Mode {
	return autonomy.FromPolicy(m.taskOptions.IntentContract, m.taskOptions.Loop)
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
