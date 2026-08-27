// Command bench is the terminal workbench for the bench filter family.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/agentexec"
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/autonomy"
	"github.com/patrickyoung/bench/internal/autoroute"
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/contractexec"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/filterexec"
	"github.com/patrickyoung/bench/internal/honeexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
	"github.com/patrickyoung/bench/internal/suite"
	"github.com/patrickyoung/bench/internal/ui"
)

const (
	version      = "0.6.6"
	maxPipeInput = 16 << 20
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	forceTUI := false
	if len(args) > 0 {
		switch args[0] {
		case "run", "ask":
			return runHeadless(args[0], args[1:], stdin, stdout, stderr)
		case "contract":
			return runContractCLI(args[1:], stdin, stdout, stderr)
		case "home":
			return runHomeCLI(args[1:], stdin, stdout, stderr)
		case "tui":
			forceTUI = true
			args = args[1:]
		case "help", "-h", "--help":
			printUsage(stdout)
			return 0
		case "version", "-V", "-version", "--version":
			fmt.Fprintln(stdout, "bench "+version)
			return 0
		}
	}
	if !forceTUI && (!isTerminalReader(stdin) || !isTerminalWriter(stdout)) {
		if len(args) == 0 {
			fmt.Fprintln(stderr, "bench: piped use needs a goal: producer | bench 'goal' · or use bench ask")
			return 2
		}
		return runHeadless("run", args, stdin, stdout, stderr)
	}

	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o tuiOptions
	fs.StringVar(&o.model, "m", os.Getenv("ASK_MODEL"), "provider/model for all model-backed stages")
	fs.StringVar(&o.workspace, "C", "", "workspace directory (default: current directory)")
	fs.Var(&o.toolbox, "t", "ply toolbox directory; PATH becomes this alone")
	fs.BoolVar(&o.shell, "sh", false, "use Ply's full-shell mode (overrides BENCH_TOOLS)")
	fs.Var(&o.skills, "s", "brief skill active at startup; repeat for more")
	fs.StringVar(&o.resume, "f", "", "verify and resume a session id or path")
	fs.StringVar(&o.resume, "session", "", "verify and resume a session id or path")
	fs.BoolVar(&o.startNew, "n", false, "start new instead of showing saved sessions")
	fs.BoolVar(&o.startNew, "new", false, "start new instead of showing saved sessions")
	fs.StringVar(&o.project, "project", "", "open an existing agent project")
	fs.StringVar(&o.home, "home", "", "open an existing built agent home")
	addTaskFlags(fs, &o.task)
	fs.Usage = func() { printUsage(stderr) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if o.shell && o.toolbox.set {
		fmt.Fprintln(stderr, "bench: -sh and -t are mutually exclusive")
		return 2
	}
	if err := validateTaskPolicy(o.task); err != nil {
		fmt.Fprintln(stderr, "bench: "+err.Error())
		return 2
	}
	if o.shell {
		o.toolbox.value = ""
	}
	if !o.toolbox.set && !o.shell {
		o.toolbox.value = strings.TrimSpace(os.Getenv("BENCH_TOOLS"))
	}
	selectedEntry := 0
	for _, selected := range []bool{o.startNew, o.resume != "", o.project != "", o.home != ""} {
		if selected {
			selectedEntry++
		}
	}
	if selectedEntry > 1 {
		fmt.Fprintln(stderr, "bench: -new, -session, -project, and -home cannot be used together")
		return 2
	}

	workspace, err := resolveWorkspace(o.workspace)
	if err != nil {
		return report(stderr, err)
	}
	root := benchDir(workspace)
	if o.task.options().ActionConfinement == plyexec.ConfinementCage {
		if err := validateCageControllerRoot(workspace, root); err != nil {
			return report(stderr, err)
		}
	}
	sessionsDir := filepath.Join(root, "sessions")
	saved, err := session.Discover(sessionsDir)
	if err != nil {
		return report(stderr, err)
	}
	initial := strings.Join(fs.Args(), " ")
	if (o.project != "" || o.home != "") && initial != "" {
		return report(stderr, errors.New("initial task cannot be combined with -project or -home"))
	}
	projectDir := ""
	if o.project != "" {
		projectDir, err = ui.ProjectPath(workspace, o.project)
		if err != nil {
			return report(stderr, err)
		}
	}
	homeDir := ""
	if o.home != "" {
		homeDir, err = ui.HomePath(workspace, o.home)
		if err != nil {
			return report(stderr, err)
		}
	}
	newPath := filepath.Join(sessionsDir, sessionName())
	active := newPath
	resuming := o.resume != ""
	if resuming {
		active = session.Resolve(sessionsDir, o.resume)
	}
	if o.task.options().ActionConfinement == plyexec.ConfinementCage {
		if err := validateCageControllerPath(workspace, active, "Ask session"); err != nil {
			return report(stderr, err)
		}
	}

	paths := filterPaths()
	askRunner := askexec.Runner{Path: paths.ask, BriefPath: paths.brief}
	plyRunner := plyexec.Runner{Path: paths.ply, AskPath: paths.ask, BriefPath: paths.brief, MayPath: paths.may, CagePath: paths.cage}
	taskRunner := contractexec.Runner{Ask: askRunner, Ply: plyRunner, MayPath: paths.may, CagePath: paths.cage}
	m := ui.New(ui.Config{
		Runner:          askRunner,
		AutoRouter:      autoroute.Runner{Ask: askRunner, Recorder: askRunner},
		ApprovalResults: askRunner,
		Recorder:        askRunner,
		Task:            taskRunner,
		Contracts:       taskRunner,
		Draft: draftexec.Runner{
			Path: paths.draft, AskPath: paths.ask, BriefPath: paths.brief,
			PlyPath: paths.ply, HonePath: paths.hone, WorkDir: workspace,
		},
		Agent: agentexec.Runner{
			Path: paths.agent, PlyPath: paths.ply, BriefPath: paths.brief,
			CagePath: paths.cage, HonePath: paths.hone, TrailPath: paths.trail,
			AskPath: paths.ask, WorkDir: workspace,
		},
		Hone:          honeexec.Runner{Path: paths.hone, AskPath: paths.ask, BriefPath: paths.brief, WorkDir: workspace},
		Brief:         briefexec.Runner{Binary: paths.brief, WorkDir: workspace},
		Ply:           plyRunner,
		Session:       active,
		NewSession:    newPath,
		Resume:        resuming,
		Choose:        !o.startNew && !resuming && projectDir == "" && initial == "" && len(saved) > 0,
		Sessions:      saved,
		Model:         strings.TrimSpace(o.model),
		Workspace:     workspace,
		DataDir:       root,
		Project:       projectDir,
		Home:          homeDir,
		InitialPrompt: initial,
		Toolbox:       o.toolbox.value,
		ActiveSkills:  append([]string(nil), o.skills...),
		TaskOptions:   o.task.options(),
		Auto: func() bool {
			mode, _ := o.task.autonomy()
			return mode == autonomy.Auto
		}(),
		MayPath:  paths.may,
		CagePath: paths.cage,
	})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return report(stderr, err)
	}
	return 0
}

// runHomeCLI is deliberately an attached pass-through. The standalone agent
// owns every subcommand, agent-home rule, and exit code; Bench only selects the
// working directory and exact suite companions.
func runHomeCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench home", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var workspace string
	fs.StringVar(&workspace, "C", "", "working directory (default: current directory)")
	fs.Usage = func() { printHomeUsage(stderr) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(fs.Args()) == 0 {
		fmt.Fprintln(stderr, "bench home: an agent command is required")
		printHomeUsage(stderr)
		return 2
	}
	work, err := resolveWorkspace(workspace)
	if err != nil {
		return report(stderr, err)
	}
	paths := filterPaths()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	outcome := (agentexec.Runner{
		Path: paths.agent, PlyPath: paths.ply, BriefPath: paths.brief,
		CagePath: paths.cage, HonePath: paths.hone, TrailPath: paths.trail,
		AskPath: paths.ask, WorkDir: work,
	}).Run(ctx, fs.Args(), stdin, stdout, stderr)
	if outcome.Err != nil {
		return report(stderr, fmt.Errorf("agent: %w", outcome.Err))
	}
	return outcome.ExitCode
}

type tuiOptions struct {
	model, workspace, resume, project, home string
	toolbox                                 trackedString
	skills                                  stringList
	shell, startNew                         bool
	task                                    taskFlags
}

func runHeadless(mode string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var model, workspace, file string
	var toolbox trackedString
	var skills stringList
	var shell bool
	var task taskFlags
	fs.StringVar(&model, "m", os.Getenv("ASK_MODEL"), "provider/model")
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "explicit replayable session file")
	fs.Var(&skills, "s", "brief skill; repeat for more")
	if mode == "run" {
		fs.Var(&toolbox, "t", "ply toolbox directory")
		fs.BoolVar(&shell, "sh", false, "use Ply's full-shell mode")
		addTaskFlags(fs, &task)
	}
	fs.Usage = func() { printHeadlessUsage(stderr, mode) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if shell && toolbox.set {
		fmt.Fprintln(stderr, "bench "+mode+": -sh and -t are mutually exclusive")
		return 2
	}
	if err := validateTaskPolicy(task); err != nil {
		fmt.Fprintln(stderr, "bench "+mode+": "+err.Error())
		return 2
	}
	if !toolbox.set && !shell {
		toolbox.value = strings.TrimSpace(os.Getenv("BENCH_TOOLS"))
	}
	if shell {
		toolbox.value = ""
	}
	work, err := resolveWorkspace(workspace)
	if err != nil {
		return report(stderr, err)
	}
	input, err := readPipe(stdin)
	if err != nil {
		return report(stderr, err)
	}
	message := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if mode == "run" && message == "" {
		fmt.Fprintln(stderr, "bench run: a goal is required; stdin is evidence for that goal")
		return 2
	}
	if mode == "ask" && message == "" && input == "" {
		fmt.Fprintln(stderr, "bench ask: pass a message or pipe stdin")
		return 2
	}
	if file == "" {
		file = filepath.Join(benchDir(work), "sessions", sessionName())
	} else if !filepath.IsAbs(file) {
		file = filepath.Join(work, file)
	}
	if task.options().ActionConfinement == plyexec.ConfinementCage {
		if err := validateCageControllerRoot(work, benchDir(work)); err != nil {
			return report(stderr, err)
		}
		if err := validateCageControllerPath(work, file, "Ask session"); err != nil {
			return report(stderr, err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	paths := filterPaths()
	if mode == "ask" {
		events := (askexec.Runner{Path: paths.ask, BriefPath: paths.brief}).Start(ctx, askexec.Request{
			Message: message, Input: input, Session: file, Skills: skills, Model: strings.TrimSpace(model),
		})
		return streamEvents(ctx, events, stdout, stderr)
	}
	plyRunner := plyexec.Runner{Path: paths.ply, AskPath: paths.ask, BriefPath: paths.brief, MayPath: paths.may, CagePath: paths.cage}
	runner := contractexec.Runner{Ask: askexec.Runner{Path: paths.ask, BriefPath: paths.brief}, Ply: plyRunner, MayPath: paths.may, CagePath: paths.cage}
	request := plyexec.TaskRequest{
		Dir: work, Goal: message, Input: input, Session: file, SubagentsDir: session.SubagentsDir(benchDir(work), file), Skills: skills,
		Toolbox: toolbox.value, Model: strings.TrimSpace(model), Options: task.options(),
	}
	if requested, _ := task.autonomy(); requested == autonomy.Auto {
		askRunner := askexec.Runner{Path: paths.ask, BriefPath: paths.brief}
		decision, code := resolveAuto(ctx, autoroute.Runner{Ask: askRunner, Recorder: askRunner}, request, stderr)
		if code != 0 {
			return code
		}
		request.Options = task.optionsFor(decision.Effective)
	}
	if request.Options.IntentContract {
		store := contractexec.FileStore{Dir: session.ContractsDir(benchDir(work), file)}
		code := streamContractDraft(ctx, runner.Compile(ctx, contractexec.DraftRequest{Task: request, Store: store}), store, stdout, stderr)
		if code == 0 {
			fmt.Fprintln(stderr, "bench run: contract awaits review; inspect the printed draft, revise or edit it, then accept it with its exact displayed digest")
			if request.Options.Loop {
				fmt.Fprintln(stderr, "bench run: Loop is invocation-scoped; select -mode loop again on contract accept or run")
			}
			return 2
		}
		return code
	}
	return streamPlyEvents(ctx, runner.Work(ctx, request), stdout, stderr)
}

func resolveAuto(ctx context.Context, router autoroute.Router, request plyexec.TaskRequest, stderr io.Writer) (autoroute.Decision, int) {
	var decision autoroute.Decision
	for event := range router.Route(ctx, autoroute.Request{Task: request, AllowQuick: true}) {
		if event.Text != "" {
			fmt.Fprint(stderr, event.Text)
		}
		if !event.Done {
			continue
		}
		if event.Err != nil || event.ExitCode != 0 || event.Decision == nil {
			if event.Err != nil {
				fmt.Fprintln(stderr, "bench: auto route:", event.Err)
			}
			if event.ExitCode != 0 {
				return decision, event.ExitCode
			}
			return decision, 1
		}
		decision = *event.Decision
	}
	if decision.Effective == "" {
		fmt.Fprintln(stderr, "bench: auto route ended without a decision")
		return decision, 1
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "bench: auto route:", ctx.Err())
		return autoroute.Decision{}, 130
	}
	reason := decision.Reason
	if decision.Clamped != "" {
		reason = decision.Clamped
	}
	fmt.Fprintf(stderr, "bench: AUTO -> %s · reason=%s\n", strings.ToUpper(string(decision.Effective)), reason)
	return decision, 0
}

// streamEvents preserves Ask's filter contract without interpreting either
// stream.
func streamEvents(ctx context.Context, events <-chan askexec.Event, stdout, stderr io.Writer) int {
	return streamOutput(ctx, func() (outputEvent, bool) {
		event, ok := <-events
		return outputEvent{Stream: event.Stream, Text: event.Text, Done: event.Done, ExitCode: event.ExitCode, Err: event.Err}, ok
	}, stdout, stderr)
}

// streamPlyEvents preserves the same filter contract while ignoring the
// terminal session metadata used only by the long-lived TUI process.
func streamPlyEvents(ctx context.Context, events <-chan plyexec.Event, stdout, stderr io.Writer) int {
	return streamOutput(ctx, func() (outputEvent, bool) {
		event, ok := <-events
		if ok && event.Contract != "" {
			return outputEvent{Stream: filterexec.Stderr, Text: event.Contract + "\n\n"}, true
		}
		return outputEvent{Stream: event.Stream, Text: event.Text, Done: event.Done, ExitCode: event.ExitCode, Err: event.Err}, ok
	}, stdout, stderr)
}

type outputEvent struct {
	Stream   filterexec.Stream
	Text     string
	Done     bool
	ExitCode int
	Err      error
}

func streamOutput(ctx context.Context, next func() (outputEvent, bool), stdout, stderr io.Writer) int {
	sawErrorText := false
	code := 1
	var finalErr error
	for {
		event, ok := next()
		if !ok {
			break
		}
		if event.Text != "" {
			var err error
			if event.Stream == filterexec.Stdout {
				_, err = io.WriteString(stdout, event.Text)
			} else {
				sawErrorText = sawErrorText || strings.TrimSpace(event.Text) != ""
				_, err = io.WriteString(stderr, event.Text)
			}
			if err != nil {
				fmt.Fprintln(stderr, "bench: writing process output:", err)
				return 1
			}
		}
		if event.Done {
			code, finalErr = event.ExitCode, event.Err
			continue
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	if finalErr != nil && !sawErrorText {
		fmt.Fprintln(stderr, "bench:", finalErr)
	}
	if code < 0 {
		return 1
	}
	return code
}

func readPipe(r io.Reader) (string, error) {
	if isTerminalReader(r) {
		return "", nil
	}
	b, err := io.ReadAll(io.LimitReader(r, maxPipeInput+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPipeInput {
		return "", fmt.Errorf("stdin exceeds %d MB", maxPipeInput>>20)
	}
	return string(b), nil
}

func isTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && isCharacterDevice(f)
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isCharacterDevice(f)
}

func isCharacterDevice(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func resolveWorkspace(dir string) (string, error) {
	var (
		path string
		err  error
	)
	if strings.TrimSpace(dir) == "" {
		path, err = os.Getwd()
	} else {
		path, err = filepath.Abs(dir)
	}
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("workspace %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", path)
	}
	return path, nil
}

func benchDir(workspace string) string {
	if root := strings.TrimSpace(os.Getenv("BENCH_DIR")); root != "" {
		if filepath.IsAbs(root) {
			return root
		}
		return filepath.Join(workspace, root)
	}
	return filepath.Join(workspace, ".bench")
}

func validateCageControllerRoot(workspace, root string) error {
	configured := strings.TrimSpace(os.Getenv("BENCH_DIR"))
	if configured == "" || !filepath.IsAbs(configured) {
		return errors.New("Cage needs an absolute external BENCH_DIR so controller sessions and contracts are outside the writable workspace; for example BENCH_DIR=$HOME/.local/state/bench/PROJECT")
	}
	rawWork, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	work := rawWork
	if resolved, resolveErr := filepath.EvalSymlinks(rawWork); resolveErr == nil {
		work = resolved
	}
	rawControl, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	control, err := prospectivePath(root)
	if err != nil {
		return fmt.Errorf("resolve BENCH_DIR for Cage: %w", err)
	}
	if pathContains(rawWork, rawControl) || pathContains(rawControl, rawWork) || pathContains(work, rawControl) || pathContains(rawControl, work) || pathContains(work, control) || pathContains(control, work) {
		return errors.New("Cage controller state must be outside and separate from the writable workspace")
	}
	return nil
}

func validateCageControllerPath(workspace, path, label string) error {
	rawWork, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	work := rawWork
	if resolved, e := filepath.EvalSymlinks(rawWork); e == nil {
		work = resolved
	}
	rawTarget, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	target, err := prospectivePath(path)
	if err != nil {
		return fmt.Errorf("resolve %s for Cage: %w", label, err)
	}
	if pathContains(rawWork, rawTarget) || pathContains(work, rawTarget) || pathContains(work, target) {
		return fmt.Errorf("Cage %s must be outside the writable workspace", label)
	}
	return nil
}

func prospectivePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur, tail := abs, []string{}
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs), nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func pathContains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type paths struct{ ask, ply, brief, draft, hone, may, cage, agent, trail string }

func filterPaths() paths {
	return paths{
		ask: toolPath("BENCH_ASK", "ask"), ply: toolPath("BENCH_PLY", "ply"),
		brief: toolPath("BENCH_BRIEF", "brief"), draft: toolPath("BENCH_DRAFT", "draft"),
		hone: toolPath("BENCH_HONE", "hone"), may: toolPath("BENCH_MAY", "may"),
		cage: toolPath("BENCH_CAGE", "cage"), agent: toolPath("BENCH_AGENT", "agent"),
		trail: toolPath("BENCH_TRAIL", "trail"),
	}
}

// toolPath preserves explicit overrides, then recognizes a release suite by
// its manifest. A bare go-installed bench therefore keeps normal PATH
// behavior, while an app can invoke a private suite's bin/bench directly and
// get the exact companions shipped beside it.
func toolPath(env, name string) string {
	if value := strings.TrimSpace(os.Getenv(env)); value != "" {
		return value
	}
	executable, err := os.Executable()
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		if candidate := suiteTool(filepath.Dir(executable), name); candidate != "" {
			return candidate
		}
	}
	return name
}

func suiteTool(binDir, name string) string {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binDir), "suite.json"))
	if err != nil {
		return ""
	}
	m, err := suite.Parse(data)
	if err != nil {
		return ""
	}
	found := false
	for _, component := range m.Components {
		if component.Name == name {
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	candidate := filepath.Join(binDir, name)
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return candidate
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

type trackedString struct {
	value string
	set   bool
}

type trackedBool struct{ value, set bool }

func (b *trackedBool) String() string { return strconv.FormatBool(b.value) }
func (b *trackedBool) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	b.value, b.set = parsed, true
	return nil
}
func (b *trackedBool) IsBoolFlag() bool { return true }

func (s *trackedString) String() string { return s.value }
func (s *trackedString) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("toolbox directory is empty")
	}
	s.value, s.set = value, true
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("skill name is empty")
	}
	*s = append(*s, value)
	return nil
}

type taskFlags struct {
	contract    bool
	mode        string
	check       string
	checkAll    bool
	approval    string
	cage        bool
	effort      string
	cycles      trackedInt
	turns       trackedInt
	timeout     trackedDuration
	compact     bool
	compactions trackedInt
}

func addTaskFlags(fs *flag.FlagSet, task *taskFlags) {
	task.cycles.name = "cycles"
	task.turns.name = "turns"
	task.compactions.name = "compactions"
	task.timeout.name = "timeout"
	fs.BoolVar(&task.contract, "contract", true, "compile intent into a replayable outcome contract before work")
	fs.StringVar(&task.mode, "mode", "", "autonomy: auto, quick, review, or loop (overrides -contract)")
	fs.StringVar(&task.check, "check", "", "literal verifier for the next outcome")
	fs.BoolVar(&task.checkAll, "check-all", false, "operator admits the configured check as judge of every contract criterion")
	fs.StringVar(&task.approval, "approval", plyexec.ApprovalOff, "execution approval: off or every-action")
	fs.BoolVar(&task.cage, "cage", false, "confine every approved model action with Cage")
	fs.StringVar(&task.effort, "effort", "", "reasoning effort passed literally through Ply to Ask")
	fs.Var(&task.cycles, "cycles", "rejected candidates before Ply stops (0 = unbounded)")
	fs.Var(&task.turns, "turns", "model turns before Ply stops (0 = unbounded)")
	fs.Var(&task.timeout, "timeout", "per-command Ply timeout")
	fs.BoolVar(&task.compact, "compact", false, "let Ply continue through full context")
	fs.Var(&task.compactions, "compactions", "Ply compactions before stopping (0 = unbounded)")
}

func (f taskFlags) options() plyexec.TaskOptions {
	return f.optionsFor(f.mustAutonomy())
}

func (f taskFlags) optionsFor(mode autonomy.Mode) plyexec.TaskOptions {
	if mode == autonomy.Auto {
		mode = autonomy.Review
	}
	options := plyexec.TaskOptions{
		IntentContract:   mode.UsesContract(),
		Loop:             mode == autonomy.Loop,
		Check:            f.check,
		CheckAllCriteria: f.checkAll,
		ApprovalPolicy:   f.approval,
		ActionConfinement: func() string {
			if f.cage {
				return plyexec.ConfinementCage
			}
			return plyexec.ConfinementOff
		}(),
		Effort: f.effort,
		Cycles: f.cycles.value, HasCycles: f.cycles.set,
		Turns: f.turns.value, HasTurns: f.turns.set,
		Timeout: f.timeout.value, HasTimeout: f.timeout.set,
		Compact:     f.compact,
		Compactions: f.compactions.value, HasCompactions: f.compactions.set,
	}
	return implyCageApproval(options)
}

func implyCageApproval(options plyexec.TaskOptions) plyexec.TaskOptions {
	if options.ActionConfinement == plyexec.ConfinementCage {
		options.ApprovalPolicy = plyexec.ApprovalEveryAction
	}
	return options
}

func (f taskFlags) autonomy() (autonomy.Mode, error) {
	if strings.TrimSpace(f.mode) != "" {
		return autonomy.Parse(f.mode)
	}
	return autonomy.FromContract(f.contract), nil
}

func (f taskFlags) mustAutonomy() autonomy.Mode {
	mode, err := f.autonomy()
	if err != nil {
		return autonomy.Review
	}
	return mode
}

func validateTaskPolicy(f taskFlags) error {
	mode, err := f.autonomy()
	if err != nil {
		return err
	}
	policy := strings.TrimSpace(f.approval)
	if policy == "" {
		policy = plyexec.ApprovalOff
	}
	if policy != plyexec.ApprovalOff && policy != plyexec.ApprovalEveryAction {
		return fmt.Errorf("approval must be %s or %s", plyexec.ApprovalOff, plyexec.ApprovalEveryAction)
	}
	if policy == plyexec.ApprovalEveryAction && mode == autonomy.Quick {
		return errors.New("every-action approval needs review or loop autonomy")
	}
	if f.cage && mode == autonomy.Quick {
		return errors.New("Cage confinement needs review or loop autonomy")
	}
	// Cage is the conservative composition: every confined action first passes
	// through the exact May approval boundary, even when -approval was omitted.
	if !f.checkAll {
		if mode == autonomy.Loop && strings.TrimSpace(f.check) == "" {
			return errors.New("loop autonomy needs a configured check")
		}
		if mode == autonomy.Loop && f.turns.set && f.turns.value == 0 {
			return errors.New("loop autonomy needs a finite positive turn budget")
		}
		return nil
	}
	if strings.TrimSpace(f.check) == "" {
		return errors.New("check-all needs a configured check")
	}
	if !mode.UsesContract() {
		return errors.New("check-all needs review or loop autonomy")
	}
	if mode == autonomy.Loop && f.turns.set && f.turns.value == 0 {
		return errors.New("loop autonomy needs a finite positive turn budget")
	}
	return nil
}

type trackedInt struct {
	name  string
	value int
	set   bool
}

func (v *trackedInt) String() string {
	if !v.set {
		return ""
	}
	return strconv.Itoa(v.value)
}

func (v *trackedInt) Set(text string) error {
	n, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("%s must be an integer", v.name)
	}
	if n < 0 {
		return fmt.Errorf("%s cannot be negative", v.name)
	}
	v.value, v.set = n, true
	return nil
}

type trackedDuration struct {
	name  string
	value time.Duration
	set   bool
}

func (v *trackedDuration) String() string {
	if !v.set {
		return ""
	}
	return v.value.String()
}

func (v *trackedDuration) Set(text string) error {
	d, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("%s must be a duration: %w", v.name, err)
	}
	if d <= 0 {
		return fmt.Errorf("%s must be positive", v.name)
	}
	v.value, v.set = d, true
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `bench — AI-assisted workbench and Unix filter

  bench [flags] [initial task]   open the interactive workbench
  bench tui [flags] [task]       force the interactive workbench
  bench run [flags] goal         auto routes once; review/loop draft; quick runs now
  bench ask [flags] [message]    Ask only; stdin message/evidence
  bench contract ...             draft, revise, edit, admit, or rerun a contract
  bench home [-C dir] COMMAND... run the standalone folder-agent executable
  bench version                  print the version
  bench help                     print this summary

Interactive flags:
  -m provider/model   model for Ask, Ply, skills, and agent builds
  -C dir              workspace (default: current directory)
  -t dir              Ply toolbox; PATH becomes this alone
  -sh                 full shell, overriding $BENCH_TOOLS
  -s skill            activate a Brief skill; repeat for more
  -f session          verify and resume a named session (-session)
  -n                   start a fresh session without the picker (-new)
  -project dir        open and check an existing agent project
  -home dir           inspect and operate an existing built agent home
  -mode auto|quick|review|loop  delegate routing, or choose immediate, reviewed, or verifier-pursuit work
  -contract           compatibility alias for review/quick
  -check command      literal verifier for the next open work outcome
  -check-all          admit that check as judge of every contract criterion
  -approval MODE      off, or every-action through exact May decisions
  -cage               confine each approved model action (needs external BENCH_DIR)
  -effort level       reasoning effort passed literally through Ply to Ask
  -cycles n           rejected candidates before Ply stops (0 = unbounded)
  -turns n            model turns before Ply stops (0 = unbounded)
  -timeout duration   per-command Ply timeout
  -compact            continue through full context by compacting
  -compactions n      compactions before Ply stops (0 = unbounded)

Inside the TUI, Enter runs and Alt/Shift+Enter inserts a newline. Type /help
for commands. Ctrl-C interrupts or quits; Ctrl-D exits an empty prompt;
Ctrl-Z suspends to the parent shell.

Ask naturally for up to three independent read-heavy subagent jobs. Bench
keeps their Ply sessions and evidence under $BENCH_DIR/subagents for inspection.

bench home is distinct from interactive /agent: the former runs an
already-built agent home; the latter promotes work into a Draft design.
bench home passes every argument after COMMAND literally to agent.

Environment: ASK_MODEL · BENCH_TOOLS · BENCH_DIR · BENCH_ASK · BENCH_PLY ·
BENCH_BRIEF · BENCH_DRAFT · BENCH_HONE · BENCH_MAY · BENCH_CAGE · BENCH_AGENT ·
BENCH_TRAIL · NO_COLOR

When stdin or stdout is not a terminal, plain bench behaves like bench run:
  git diff | bench -m provider/model 'review this patch'`)
}

func printHomeUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: bench home [-C dir] COMMAND [agent arguments...]

Run the standalone agent executable with its public argv/stdin/stdout/stderr
contract intact. Bench supplies the exact Ply, Brief, Cage, and Hone programs
from the active suite. Use -- before an agent command that begins with a dash.

examples:
  bench home show support-chief
  bench home run -m provider/model support-chief
  bench home specialist support-chief researcher -- 'bounded question'
  bench home learn -into triage support-chief SESSION.jsonl
  bench home history support-chief check`)
}

func printHeadlessUsage(w io.Writer, mode string) {
	if mode == "run" {
		fmt.Fprintln(w, "usage: bench run [-m model] [-effort level] [-C dir] [-t tools | -sh] [-s skill] [-f session] [-mode auto|quick|review|loop] [-check command [-check-all]] [-approval off|every-action] [-cage] [-cycles n] [-turns n] [-timeout duration] [-compact [-compactions n]] goal")
		fmt.Fprintln(w, "auto may select immediate Quick with the current tool grant; review drafts and exits 2; loop drafts a checked contract")
		return
	}
	fmt.Fprintln(w, "usage: bench ask [-m model] [-C dir] [-s skill] [-f session] [message]")
}

func sessionName() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return time.Now().Format("20060102-150405.000000000") + ".jsonl"
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:]) + ".jsonl"
}

func report(w io.Writer, err error) int {
	fmt.Fprintln(w, "bench:", err)
	return 1
}
