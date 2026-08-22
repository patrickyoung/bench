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
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/filterexec"
	"github.com/patrickyoung/bench/internal/honeexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
	"github.com/patrickyoung/bench/internal/suite"
	"github.com/patrickyoung/bench/internal/ui"
)

const (
	version      = "0.5.0"
	maxPipeInput = 16 << 20
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	forceTUI := false
	if len(args) > 0 {
		switch args[0] {
		case "run", "ask":
			return runHeadless(args[0], args[1:], stdin, stdout, stderr)
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
	if o.shell {
		o.toolbox.value = ""
	}
	if !o.toolbox.set && !o.shell {
		o.toolbox.value = strings.TrimSpace(os.Getenv("BENCH_TOOLS"))
	}
	if o.startNew && (o.resume != "" || o.project != "") || o.resume != "" && o.project != "" {
		fmt.Fprintln(stderr, "bench: -new, -session, and -project cannot be used together")
		return 2
	}

	workspace, err := resolveWorkspace(o.workspace)
	if err != nil {
		return report(stderr, err)
	}
	root := benchDir(workspace)
	sessionsDir := filepath.Join(root, "sessions")
	saved, err := session.Discover(sessionsDir)
	if err != nil {
		return report(stderr, err)
	}
	initial := strings.Join(fs.Args(), " ")
	if o.project != "" && initial != "" {
		return report(stderr, errors.New("initial task cannot be combined with -project"))
	}
	projectDir := ""
	if o.project != "" {
		projectDir, err = ui.ProjectPath(workspace, o.project)
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

	paths := filterPaths()
	plyRunner := plyexec.Runner{Path: paths.ply, AskPath: paths.ask, BriefPath: paths.brief}
	m := ui.New(ui.Config{
		Runner: askexec.Runner{Path: paths.ask, BriefPath: paths.brief},
		Task:   plyRunner,
		Draft: draftexec.Runner{
			Path: paths.draft, AskPath: paths.ask, BriefPath: paths.brief,
			PlyPath: paths.ply, HonePath: paths.hone, WorkDir: workspace,
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
		InitialPrompt: initial,
		Toolbox:       o.toolbox.value,
		ActiveSkills:  append([]string(nil), o.skills...),
		TaskOptions:   o.task.options(),
	})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return report(stderr, err)
	}
	return 0
}

type tuiOptions struct {
	model, workspace, resume, project string
	toolbox                           trackedString
	skills                            stringList
	shell, startNew                   bool
	task                              taskFlags
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	paths := filterPaths()
	if mode == "ask" {
		events := (askexec.Runner{Path: paths.ask, BriefPath: paths.brief}).Start(ctx, askexec.Request{
			Message: message, Input: input, Session: file, Skills: skills, Model: strings.TrimSpace(model),
		})
		return streamEvents(ctx, events, stdout, stderr)
	}
	events := (plyexec.Runner{Path: paths.ply, AskPath: paths.ask, BriefPath: paths.brief}).Work(ctx, plyexec.TaskRequest{
		Dir: work, Goal: message, Input: input, Session: file, SubagentsDir: session.SubagentsDir(benchDir(work), file), Skills: skills,
		Toolbox: toolbox.value, Model: strings.TrimSpace(model), Options: task.options(),
	})
	return streamPlyEvents(ctx, events, stdout, stderr)
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
		if event.Done {
			code, finalErr = event.ExitCode, event.Err
			continue
		}
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

type paths struct{ ask, ply, brief, draft, hone string }

func filterPaths() paths {
	return paths{
		ask: toolPath("BENCH_ASK", "ask"), ply: toolPath("BENCH_PLY", "ply"),
		brief: toolPath("BENCH_BRIEF", "brief"), draft: toolPath("BENCH_DRAFT", "draft"),
		hone: toolPath("BENCH_HONE", "hone"),
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
	check       string
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
	fs.StringVar(&task.check, "check", "", "literal shell-backed Ply check")
	fs.StringVar(&task.effort, "effort", "", "reasoning effort passed literally through Ply to Ask")
	fs.Var(&task.cycles, "cycles", "failed checks before Ply stops (0 = unbounded)")
	fs.Var(&task.turns, "turns", "model turns before Ply stops (0 = unbounded)")
	fs.Var(&task.timeout, "timeout", "per-command Ply timeout")
	fs.BoolVar(&task.compact, "compact", false, "let Ply continue through full context")
	fs.Var(&task.compactions, "compactions", "Ply compactions before stopping (0 = unbounded)")
}

func (f taskFlags) options() plyexec.TaskOptions {
	return plyexec.TaskOptions{
		Check:  f.check,
		Effort: f.effort,
		Cycles: f.cycles.value, HasCycles: f.cycles.set,
		Turns: f.turns.value, HasTurns: f.turns.set,
		Timeout: f.timeout.value, HasTimeout: f.timeout.set,
		Compact:     f.compact,
		Compactions: f.compactions.value, HasCompactions: f.compactions.set,
	}
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
  bench run [flags] goal         Ask + Ply; stdin evidence, answer stdout
  bench ask [flags] [message]    Ask only; stdin message/evidence
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
  -check command      literal shell-backed check for each open task turn
  -effort level       reasoning effort passed literally through Ply to Ask
  -cycles n           failed checks before Ply stops (0 = unbounded)
  -turns n            model turns before Ply stops (0 = unbounded)
  -timeout duration   per-command Ply timeout
  -compact            continue through full context by compacting
  -compactions n      compactions before Ply stops (0 = unbounded)

Inside the TUI, Enter runs and Alt/Shift+Enter inserts a newline. Type /help
for commands. Ctrl-C interrupts or quits; Ctrl-D exits an empty prompt;
Ctrl-Z suspends to the parent shell.

Ask naturally for up to three independent read-heavy subagent jobs. Bench
keeps their Ply sessions and evidence under $BENCH_DIR/subagents for inspection.

Environment: ASK_MODEL · BENCH_TOOLS · BENCH_DIR · BENCH_ASK · BENCH_PLY ·
BENCH_BRIEF · BENCH_DRAFT · BENCH_HONE · NO_COLOR

When stdin or stdout is not a terminal, plain bench behaves like bench run:
  git diff | bench -m provider/model 'review this patch'`)
}

func printHeadlessUsage(w io.Writer, mode string) {
	if mode == "run" {
		fmt.Fprintln(w, "usage: bench run [-m model] [-effort level] [-C dir] [-t tools | -sh] [-s skill] [-f session] [-check command] [-cycles n] [-turns n] [-timeout duration] [-compact [-compactions n]] goal")
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
