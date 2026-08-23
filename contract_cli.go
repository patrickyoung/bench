package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/contractexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
)

func runContractCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printContractUsage(stderr)
		return 2
	}
	switch args[0] {
	case "draft", "new":
		return runContractDraft(args[1:], stdin, stdout, stderr)
	case "revise":
		return runContractRevise(args[1:], stdin, stdout, stderr)
	case "show", "status":
		return runContractShow(args[1:], stdout, stderr)
	case "edit":
		return runContractEdit(args[1:], stdin, stdout, stderr)
	case "import":
		return runContractImport(args[1:], stdout, stderr)
	case "accept", "admit":
		return runContractAccept(args[1:], stdout, stderr)
	case "run":
		return runContractRun(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printContractUsage(stdout)
		return 0
	default:
		fmt.Fprintln(stderr, "bench contract: unknown command:", args[0])
		printContractUsage(stderr)
		return 2
	}
}

func runContractDraft(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract draft", flag.ContinueOnError)
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
	fs.Var(&toolbox, "t", "ply toolbox directory bound to later work")
	fs.BoolVar(&shell, "sh", false, "bind later work to full-shell mode")
	task.contract = true
	fs.StringVar(&task.check, "check", "", "literal verifier bound to later work")
	fs.BoolVar(&task.checkAll, "check-all", false, "operator admits the configured check as judge of every contract criterion")
	fs.StringVar(&task.effort, "effort", "", "reasoning effort for the Ask contract compiler")
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if shell && toolbox.set {
		fmt.Fprintln(stderr, "bench contract draft: -sh and -t are mutually exclusive")
		return 2
	}
	if err := validateCheckAllFlags(task); err != nil {
		fmt.Fprintln(stderr, "bench contract draft:", err)
		return 2
	}
	goal := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if goal == "" {
		fmt.Fprintln(stderr, "bench contract draft: an outcome is required")
		return 2
	}
	work, resolvedFile, store, err := contractCLIPaths(workspace, file, true)
	if err != nil {
		return report(stderr, err)
	}
	input, err := readPipe(stdin)
	if err != nil {
		return report(stderr, err)
	}
	if !toolbox.set && !shell {
		toolbox.value = strings.TrimSpace(os.Getenv("BENCH_TOOLS"))
	}
	if shell {
		toolbox.value = ""
	}
	paths := filterPaths()
	runner := contractexec.Runner{Ask: askexec.Runner{Path: paths.ask, BriefPath: paths.brief}}
	req := plyexec.TaskRequest{
		Dir: work, Goal: goal, Input: input, Session: resolvedFile,
		SubagentsDir: session.SubagentsDir(benchDir(work), resolvedFile), Skills: skills,
		Toolbox: toolbox.value, Model: strings.TrimSpace(model), Options: task.options(),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return streamContractDraft(ctx, runner.Compile(ctx, contractexec.DraftRequest{Task: req, Store: store}), store, stdout, stderr)
}

func runContractRevise(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract revise", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var model, workspace, file, effort string
	var toolbox trackedString
	var shell bool
	fs.StringVar(&model, "m", os.Getenv("ASK_MODEL"), "provider/model")
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "session file whose contract should be revised")
	fs.StringVar(&effort, "effort", "", "reasoning effort")
	fs.Var(&toolbox, "t", "replace the draft's Ply toolbox binding")
	fs.BoolVar(&shell, "sh", false, "replace the toolbox binding with full-shell mode")
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if shell && toolbox.set {
		fmt.Fprintln(stderr, "bench contract revise: -sh and -t are mutually exclusive")
		return 2
	}
	work, resolvedFile, store, err := contractCLIPaths(workspace, file, false)
	if err != nil {
		return report(stderr, err)
	}
	instruction := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if instruction == "" {
		instruction, err = readPipe(stdin)
		if err != nil {
			return report(stderr, err)
		}
		instruction = strings.TrimSpace(instruction)
	}
	if instruction == "" {
		fmt.Fprintln(stderr, "bench contract revise: pass a revision instruction or pipe one on stdin")
		return 2
	}
	current, status, err := store.Load()
	if err != nil {
		return report(stderr, err)
	}
	if status != "draft" {
		current.ParentRevisionID = current.RevisionID
	}
	if !toolbox.set {
		toolbox.value = current.Toolbox
	}
	if shell {
		toolbox.value = ""
	}
	options := plyexec.TaskOptions{
		IntentContract: true, Check: current.Check, CheckAllCriteria: current.CheckAll, Effort: effort,
	}
	paths := filterPaths()
	runner := contractexec.Runner{Ask: askexec.Runner{Path: paths.ask, BriefPath: paths.brief}}
	req := plyexec.TaskRequest{
		Dir: work, Goal: current.Intent, Session: resolvedFile,
		SubagentsDir: session.SubagentsDir(benchDir(work), resolvedFile), Skills: append([]string{}, current.Skills...),
		Toolbox: toolbox.value, Model: strings.TrimSpace(model), Options: options,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return streamContractDraft(ctx, runner.Compile(ctx, contractexec.DraftRequest{
		Task: req, Current: &current, Instruction: instruction, Store: store,
	}), store, stdout, stderr)
}

func runContractShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var workspace, file string
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "session file")
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "bench contract show: unexpected arguments")
		return 2
	}
	_, resolvedFile, store, err := contractCLIPaths(workspace, file, false)
	if err != nil {
		return report(stderr, err)
	}
	draft, status, err := store.Load()
	if err != nil {
		return report(stderr, err)
	}
	body, err := os.ReadFile(store.DraftPath())
	if err != nil {
		return report(stderr, err)
	}
	exactDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	if exactDigest != draft.DraftSHA256 {
		return report(stderr, errors.New("editable contract changed while it was being shown; run show again"))
	}
	if _, err := stdout.Write(body); err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stderr, "bench contract: %s · generation %d · exact draft.json %s · session %s\n", status, draft.Generation, draft.DraftSHA256, resolvedFile)
	if status == "draft" {
		fmt.Fprintln(stderr, "bench contract: editable", store.DraftPath())
	}
	return 0
}

func runContractEdit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var workspace, file string
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "session file")
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "bench contract edit: unexpected arguments")
		return 2
	}
	work, resolvedFile, store, err := contractCLIPaths(workspace, file, false)
	if err != nil {
		return report(stderr, err)
	}
	current, status, err := store.Load()
	if err != nil {
		return report(stderr, err)
	}
	if status == "admitted" {
		current.Generation++
		current.ParentRevisionID = current.RevisionID
		current.RevisionID = ""
		current.ContractID = ""
		if _, err := store.SaveDraftCAS(current, current.DraftSHA256); err != nil {
			return report(stderr, fmt.Errorf("open contract amendment: %w", err))
		}
	} else if status != "draft" {
		return report(stderr, errors.New("contract is not in an editable state"))
	}
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], store.DraftPath())...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = work, stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		return report(stderr, fmt.Errorf("contract editor: %w", err))
	}
	return importContractDraft(ctx, resolvedFile, store, stderr)
}

func runContractImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var workspace, file string
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "session file")
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "bench contract import: unexpected arguments")
		return 2
	}
	_, resolvedFile, store, err := contractCLIPaths(workspace, file, false)
	if err != nil {
		return report(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return importContractDraft(ctx, resolvedFile, store, stderr)
}

func importContractDraft(ctx context.Context, sessionPath string, store contractexec.FileStore, stderr io.Writer) int {
	paths := filterPaths()
	runner := contractexec.Runner{Ask: askexec.Runner{Path: paths.ask, BriefPath: paths.brief}}
	draft, err := runner.Import(ctx, contractexec.ImportRequest{Session: sessionPath, Store: store})
	if err != nil {
		return report(stderr, fmt.Errorf("edited contract is invalid; file was left for correction: %w", err))
	}
	fmt.Fprintf(stderr, "bench contract: manual draft r%d sealed · %s · Ply has not started\n", draft.Generation, draft.DraftSHA256)
	return 0
}

func runContractAccept(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract accept", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var model, workspace, file, expect string
	var toolbox trackedString
	var shell bool
	var task taskFlags
	fs.StringVar(&model, "m", os.Getenv("ASK_MODEL"), "provider/model for admitted work")
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "session file")
	fs.StringVar(&expect, "expect", "", "exact displayed draft sha256")
	fs.Var(&toolbox, "t", "ply toolbox directory")
	fs.BoolVar(&shell, "sh", false, "use Ply's full-shell mode")
	addContractRuntimeFlags(fs, &task)
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "bench contract accept: unexpected arguments")
		return 2
	}
	if strings.TrimSpace(expect) == "" {
		fmt.Fprintln(stderr, "bench contract accept: -expect DRAFT_SHA256 is required")
		return 2
	}
	if shell && toolbox.set {
		fmt.Fprintln(stderr, "bench contract accept: -sh and -t are mutually exclusive")
		return 2
	}
	work, resolvedFile, store, err := contractCLIPaths(workspace, file, false)
	if err != nil {
		return report(stderr, err)
	}
	draft, status, err := store.Load()
	if err != nil {
		return report(stderr, err)
	}
	if status != "draft" {
		fmt.Fprintln(stderr, "bench contract accept: no draft is awaiting admission")
		return 2
	}
	if work != draft.Workspace {
		fmt.Fprintln(stderr, "bench contract accept: workspace differs from the drafted contract")
		return 2
	}
	if !toolbox.set && !shell {
		toolbox.value = draft.Toolbox
	}
	if shell {
		toolbox.value = ""
	}
	options := task.options()
	options.IntentContract = true
	options.Check = draft.Check
	options.CheckAllCriteria = draft.CheckAll
	paths := filterPaths()
	plyRunner := plyexec.Runner{Path: paths.ply, AskPath: paths.ask, BriefPath: paths.brief}
	runner := contractexec.Runner{Ask: askexec.Runner{Path: paths.ask, BriefPath: paths.brief}, Ply: plyRunner}
	req := plyexec.TaskRequest{
		Dir: work, Goal: draft.Intent, Session: resolvedFile,
		SubagentsDir: session.SubagentsDir(benchDir(work), resolvedFile), Skills: append([]string{}, draft.Skills...),
		Toolbox: toolbox.value, Model: strings.TrimSpace(model), Options: options,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	events := runner.Admit(ctx, contractexec.AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: expect,
	})
	return streamPlyEvents(ctx, events, stdout, stderr)
}

func runContractRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench contract run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var model, workspace, file string
	var toolbox trackedString
	var shell bool
	var task taskFlags
	fs.StringVar(&model, "m", os.Getenv("ASK_MODEL"), "provider/model for admitted work")
	fs.StringVar(&workspace, "C", "", "workspace directory")
	fs.StringVar(&file, "f", "", "session file")
	fs.Var(&toolbox, "t", "ply toolbox directory")
	fs.BoolVar(&shell, "sh", false, "use Ply's full-shell mode")
	addContractRuntimeFlags(fs, &task)
	if err := fs.Parse(args); err != nil {
		return flagCode(err)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "bench contract run: unexpected arguments")
		return 2
	}
	if shell && toolbox.set {
		fmt.Fprintln(stderr, "bench contract run: -sh and -t are mutually exclusive")
		return 2
	}
	work, resolvedFile, store, err := contractCLIPaths(workspace, file, false)
	if err != nil {
		return report(stderr, err)
	}
	draft, status, err := store.Load()
	if err != nil {
		return report(stderr, err)
	}
	if status != "admitted" {
		fmt.Fprintln(stderr, "bench contract run: the contract has not been admitted")
		return 2
	}
	if work != draft.Workspace {
		fmt.Fprintln(stderr, "bench contract run: workspace differs from the admitted contract")
		return 2
	}
	if !toolbox.set && !shell {
		toolbox.value = draft.Toolbox
	}
	if shell {
		toolbox.value = ""
	}
	options := task.options()
	options.IntentContract = true
	options.Check = draft.Check
	options.CheckAllCriteria = draft.CheckAll
	paths := filterPaths()
	runner := contractexec.Runner{
		Ask: askexec.Runner{Path: paths.ask, BriefPath: paths.brief},
		Ply: plyexec.Runner{Path: paths.ply, AskPath: paths.ask, BriefPath: paths.brief},
	}
	req := plyexec.TaskRequest{
		Dir: work, Goal: draft.Intent, Session: resolvedFile,
		SubagentsDir: session.SubagentsDir(benchDir(work), resolvedFile), Skills: append([]string{}, draft.Skills...),
		Toolbox: toolbox.value, Model: strings.TrimSpace(model), Options: options,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return streamPlyEvents(ctx, runner.Run(ctx, contractexec.RunRequest{Task: req, Draft: draft, Store: store}), stdout, stderr)
}

func contractCLIPaths(workspace, file string, create bool) (string, string, contractexec.FileStore, error) {
	work, err := resolveWorkspace(workspace)
	if err != nil {
		return "", "", contractexec.FileStore{}, err
	}
	if strings.TrimSpace(file) == "" {
		if !create {
			return "", "", contractexec.FileStore{}, errors.New("-f SESSION is required")
		}
		file = filepath.Join(benchDir(work), "sessions", sessionName())
	} else if !filepath.IsAbs(file) {
		file = filepath.Join(work, file)
	}
	store := contractexec.FileStore{Dir: session.ContractsDir(benchDir(work), file)}
	return work, file, store, nil
}

// Contract authority and verifier policy come only from the reviewed draft.
// Runtime flags tune the admitted Ply attempt but cannot silently replace the
// policy whose exact bytes the user admitted.
func addContractRuntimeFlags(fs *flag.FlagSet, task *taskFlags) {
	task.cycles.name = "cycles"
	task.turns.name = "turns"
	task.compactions.name = "compactions"
	task.timeout.name = "timeout"
	fs.StringVar(&task.effort, "effort", "", "reasoning effort passed literally through Ply to Ask")
	fs.Var(&task.cycles, "cycles", "rejected candidates before Ply stops (0 = unbounded)")
	fs.Var(&task.turns, "turns", "model turns before Ply stops (0 = unbounded)")
	fs.Var(&task.timeout, "timeout", "per-command Ply timeout")
	fs.BoolVar(&task.compact, "compact", false, "let Ply continue through full context")
	fs.Var(&task.compactions, "compactions", "Ply compactions before stopping (0 = unbounded)")
}

func streamContractDraft(ctx context.Context, events <-chan contractexec.DraftEvent, store contractexec.FileStore, stdout, stderr io.Writer) int {
	var draft *contractexec.Draft
	code := 1
	var finalErr error
	for event := range events {
		if event.Text != "" {
			if _, err := io.WriteString(stderr, event.Text); err != nil {
				return report(stderr, err)
			}
		}
		if event.Done {
			code, finalErr, draft = event.ExitCode, event.Err, event.Draft
		}
	}
	if finalErr != nil || code != 0 || draft == nil {
		if finalErr != nil {
			fmt.Fprintln(stderr, "bench contract:", finalErr)
		}
		return code
	}
	fmt.Fprintln(stdout, store.DraftPath())
	fmt.Fprintf(stderr, "bench contract: draft r%d · %s · Ply has not started\n", draft.Generation, draft.DraftSHA256)
	return 0
}

func flagCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

func printContractUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  bench contract draft  [OPTIONS] OUTCOME
  bench contract revise -f SESSION [OPTIONS] CHANGE
  bench contract show   -f SESSION
  bench contract edit   -f SESSION
  bench contract import -f SESSION
  bench contract accept -f SESSION -expect SHA256 [OPTIONS]
  bench contract run    -f SESSION [OPTIONS]

Draft and revise use Ask plus the selected Brief skills but never invoke Ply.
The printed draft.json is ordinary editable JSON; import validates and seals
changes made by an external editor. Accept checks the exact
displayed digest, seals the immutable admission, and only then starts Ply.`)
}
