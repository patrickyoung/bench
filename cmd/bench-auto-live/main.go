// Command bench-auto-live runs a small, developer-only live experiment over
// Bench's public CLI. It adds no product telemetry or execution authority.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	caseSchema   = "bench.auto-live/case/v1"
	runSchema    = "bench.auto-live/run/v1"
	resultSchema = "bench.auto-live/result/v1"
	maxLine      = 1 << 20
)

var experimentUnset = map[string]bool{
	"ASK_DIR": true, "ASK_MODEL": true, "ASK_SYSTEM": true,
	"BENCH_CAGE": true, "BENCH_MAY": true, "BENCH_TOOLS": true,
	"CAGE": true, "MAY": true,
	"PLY_CONTRACT_ID": true, "PLY_DEPTH": true, "PLY_DIR": true,
	"PLY_EFFORT": true, "PLY_MAY_JOB": true, "PLY_SHELL": true, "PLY_TOOLS": true,
}

type caseSpec struct {
	Schema   string `json:"schema"`
	ID       string `json:"id"`
	Class    string `json:"class"`
	Intent   string `json:"intent"`
	Fixture  string `json:"fixture"`
	Check    bool   `json:"check"`
	CheckAll bool   `json:"check_all"`
}

type phase struct {
	Name         string `json:"name"`
	Exit         int    `json:"exit"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	StdoutSHA256 string `json:"stdout_sha256"`
	StderrSHA256 string `json:"stderr_sha256"`
}

type arm struct {
	CaseID             string `json:"case_id"`
	Class              string `json:"class"`
	Arm                string `json:"arm"`
	Order              int    `json:"order"`
	Workspace          string `json:"workspace"`
	WorkspacePhysical  string `json:"workspace_physical"`
	Control            string `json:"control"`
	ControlPhysical    string `json:"control_physical"`
	BasePhysical       string `json:"base_physical"`
	Session            string `json:"session"`
	Effects            string `json:"effects"`
	PlyTrace           string `json:"ply_trace"`
	Wrapper            string `json:"wrapper"`
	WrapperSHA256      string `json:"wrapper_sha256"`
	WorkspaceBefore    string `json:"workspace_before_sha256"`
	SelectedRoute      string `json:"selected_route,omitempty"`
	RouteClamp         string `json:"route_clamp,omitempty"`
	RouteRecordSHA256  string `json:"route_record_sha256,omitempty"`
	DraftSHA256        string `json:"draft_sha256,omitempty"`
	AcceptScript       string `json:"accept_script,omitempty"`
	AcceptScriptSHA256 string `json:"accept_script_sha256,omitempty"`
	Initial            phase  `json:"initial"`
}

type runManifest struct {
	Schema         string        `json:"schema"`
	Created        string        `json:"created"`
	CasesPath      string        `json:"cases_path"`
	CasesSHA256    string        `json:"cases_sha256"`
	ToolboxPath    string        `json:"toolbox_path"`
	ToolboxSHA256  string        `json:"toolbox_sha256"`
	OraclePath     string        `json:"oracle_path"`
	OracleSHA256   string        `json:"oracle_sha256"`
	ExpectedPath   string        `json:"expected_path"`
	ExpectedSHA256 string        `json:"expected_sha256"`
	BenchPath      string        `json:"bench_path"`
	BenchSHA256    string        `json:"bench_sha256"`
	AskPath        string        `json:"ask_path"`
	AskSHA256      string        `json:"ask_sha256"`
	PlyPath        string        `json:"ply_path"`
	PlySHA256      string        `json:"ply_sha256"`
	Model          string        `json:"model"`
	Effort         string        `json:"effort"`
	Fixtures       []inputDigest `json:"fixtures"`
	Arms           []arm         `json:"arms"`
}

type inputDigest struct {
	CaseID string `json:"case_id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type result struct {
	Schema               string   `json:"schema"`
	CaseID               string   `json:"case_id"`
	Class                string   `json:"class"`
	Arm                  string   `json:"arm"`
	Order                int      `json:"order"`
	SelectedRoute        string   `json:"selected_route,omitempty"`
	RouteClamp           string   `json:"route_clamp,omitempty"`
	RouteRecordSHA256    string   `json:"route_record_sha256,omitempty"`
	AdmissionSHA256      string   `json:"admission_sha256,omitempty"`
	ContractResultSHA256 string   `json:"contract_result_sha256,omitempty"`
	TerminalStatus       string   `json:"terminal_status,omitempty"`
	Admitted             bool     `json:"admitted"`
	PlyInvocations       int      `json:"ply_invocations"`
	WorkspaceBefore      string   `json:"workspace_before_sha256"`
	WorkspaceAfter       string   `json:"workspace_after_sha256"`
	Effects              int      `json:"effects"`
	ReplaySHA256         string   `json:"replay_sha256"`
	OracleExit           int      `json:"oracle_exit"`
	Verdict              string   `json:"verdict"`
	Failures             []string `json:"failures"`
	Initial              phase    `json:"initial"`
	Admission            *phase   `json:"admission,omitempty"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "prepare":
		return prepare(args[1:], stdout, stderr)
	case "score":
		return score(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintln(stderr, "bench-auto-live: use prepare or score")
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: bench-auto-live prepare -bench PATH -ask PATH -ply PATH -model MODEL -out NEWDIR -expect sha256:CASES")
	fmt.Fprintln(w, "       bench-auto-live score -out DIR")
}

func prepare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench-auto-live prepare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var bench, ask, ply, model, effort, out, casesPath, expect string
	fs.StringVar(&bench, "bench", "bench", "Bench executable")
	fs.StringVar(&ask, "ask", "ask", "Ask executable")
	fs.StringVar(&ply, "ply", "ply", "Ply executable")
	fs.StringVar(&model, "model", "", "provider/model")
	fs.StringVar(&effort, "effort", "", "reasoning effort")
	fs.StringVar(&out, "out", "", "new result directory")
	fs.StringVar(&casesPath, "cases", "eval/auto/live/cases.jsonl", "case manifest")
	fs.StringVar(&expect, "expect", "", "exact case-manifest digest")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(model) == "" || strings.TrimSpace(out) == "" || strings.TrimSpace(expect) == "" {
		fs.Usage()
		return 2
	}
	casesPath, err := filepath.Abs(casesPath)
	if err != nil {
		return broken(stderr, err)
	}
	caseBytes, err := os.ReadFile(casesPath)
	if err != nil {
		return broken(stderr, err)
	}
	if got := digestBytes(caseBytes); got != expect {
		return broken(stderr, fmt.Errorf("case manifest digest %s does not match -expect %s", got, expect))
	}
	cases, err := readCases(caseBytes)
	if err != nil {
		return broken(stderr, err)
	}
	root := filepath.Dir(casesPath)
	toolbox := filepath.Join(root, "toolbox")
	oracle := filepath.Join(root, "oracle.sh")
	expected := filepath.Join(root, "expected")
	bench, err = executable(bench)
	if err != nil {
		return broken(stderr, err)
	}
	ask, err = executable(ask)
	if err != nil {
		return broken(stderr, err)
	}
	ply, err = executable(ply)
	if err != nil {
		return broken(stderr, err)
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return broken(stderr, err)
	}
	if overlap, err := pathsOverlap(out, root); err != nil {
		return broken(stderr, err)
	} else if overlap {
		return broken(stderr, errors.New("result directory must be outside the live corpus tree"))
	}
	manifest := runManifest{
		Schema: runSchema, Created: time.Now().UTC().Format(time.RFC3339Nano), CasesPath: casesPath, CasesSHA256: digestBytes(caseBytes),
		ToolboxPath: toolbox, OraclePath: oracle, ExpectedPath: expected, BenchPath: bench, AskPath: ask, PlyPath: ply, Model: model, Effort: effort,
	}
	for path, dst := range map[string]*string{toolbox: &manifest.ToolboxSHA256, oracle: &manifest.OracleSHA256, expected: &manifest.ExpectedSHA256, bench: &manifest.BenchSHA256, ask: &manifest.AskSHA256, ply: &manifest.PlySHA256} {
		value, hashErr := pathDigest(path)
		if hashErr != nil {
			return broken(stderr, hashErr)
		}
		*dst = value
	}
	for _, c := range cases {
		path := filepath.Join(root, c.Fixture)
		digest, err := pathDigest(path)
		if err != nil {
			return broken(stderr, fmt.Errorf("fixture %s: %w", c.ID, err))
		}
		manifest.Fixtures = append(manifest.Fixtures, inputDigest{CaseID: c.ID, Path: path, SHA256: digest})
	}
	if err := mkdirNew(out); err != nil {
		return broken(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	order := 0
	for _, c := range cases {
		arms := []string{"auto"}
		if c.Class != "consequential" {
			arms = append(arms, "review")
		}
		for _, name := range arms {
			if ctx.Err() != nil {
				return 130
			}
			order++
			fmt.Fprintf(stderr, "bench-auto-live: prepare %s/%s\n", c.ID, name)
			a, prepErr := prepareArm(ctx, out, root, toolbox, oracle, bench, ask, ply, model, effort, c, name, order)
			if prepErr != nil {
				if errors.Is(prepErr, context.Canceled) {
					return 130
				}
				return broken(stderr, prepErr)
			}
			manifest.Arms = append(manifest.Arms, a)
			if err := writeJSON(filepath.Join(out, "run.json"), manifest, 0o600); err != nil {
				return broken(stderr, err)
			}
		}
	}
	if err := writePlanDigest(out); err != nil {
		return broken(stderr, err)
	}
	if err := writeNext(out, manifest); err != nil {
		return broken(stderr, err)
	}
	fmt.Fprintln(stdout, filepath.Join(out, "NEXT.tsv"))
	fmt.Fprintln(stderr, "bench-auto-live: inspect each listed draft and run its literal accept script; consequential cases have no accept script")
	return 0
}

func prepareArm(ctx context.Context, out, root, toolbox, oracle, bench, ask, ply, model, effort string, c caseSpec, name string, order int) (arm, error) {
	base := filepath.Join(out, c.ID, strings.ToUpper(name))
	workspace, control := filepath.Join(base, "workspace"), filepath.Join(base, "control")
	if err := os.MkdirAll(control, 0o700); err != nil {
		return arm{}, err
	}
	if err := copyTree(filepath.Join(root, c.Fixture), workspace); err != nil {
		return arm{}, fmt.Errorf("copy fixture %s: %w", c.ID, err)
	}
	basePhysical, err := filepath.EvalSymlinks(base)
	if err != nil {
		return arm{}, err
	}
	workspacePhysical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return arm{}, err
	}
	controlPhysical, err := filepath.EvalSymlinks(control)
	if err != nil {
		return arm{}, err
	}
	before, err := treeDigest(workspace)
	if err != nil {
		return arm{}, err
	}
	sessionPath := filepath.Join(control, "session.jsonl")
	effects, trace := filepath.Join(control, "effects.log"), filepath.Join(control, "ply.trace")
	if err := os.WriteFile(effects, nil, 0o600); err != nil {
		return arm{}, err
	}
	if err := os.WriteFile(trace, nil, 0o600); err != nil {
		return arm{}, err
	}
	wrapper := filepath.Join(control, "ply-trace")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nset -eu\ntrace=$BENCH_LIVE_PLY_TRACE\nreal=$BENCH_LIVE_REAL_PLY\nunset BENCH_LIVE_PLY_TRACE BENCH_LIVE_REAL_PLY\nprintf 'ply\\n' >> \"$trace\"\nexec \"$real\" \"$@\"\n"), 0o700); err != nil {
		return arm{}, err
	}
	wrapperHash, err := pathDigest(wrapper)
	if err != nil {
		return arm{}, err
	}
	check := ""
	if c.Check {
		check = shellQuote(oracle) + " check " + shellQuote(c.ID)
	}
	argv := []string{"run", "-C", workspace, "-t", toolbox, "-f", sessionPath, "-mode", name, "-m", model, "-turns", "20", "-cycles", "3", "-timeout", "60s"}
	if effort != "" {
		argv = append(argv, "-effort", effort)
	}
	if check != "" {
		argv = append(argv, "-check", check)
		if c.CheckAll {
			argv = append(argv, "-check-all")
		}
	}
	argv = append(argv, "--", c.Intent)
	env := experimentEnv(os.Environ(), map[string]string{
		"BENCH_DIR": filepath.Join(control, "bench"), "BENCH_ASK": ask, "BENCH_PLY": wrapper,
		"BENCH_LIVE_REAL_PLY": ply, "BENCH_LIVE_PLY_TRACE": trace, "BENCH_LIVE_EFFECTS": effects, "NO_COLOR": "1",
	})
	initial, err := execute(ctx, bench, argv, env, workspace, "initial", filepath.Join(base, "initial.stdout"), filepath.Join(base, "initial.stderr"))
	if err != nil {
		return arm{}, err
	}
	a := arm{CaseID: c.ID, Class: c.Class, Arm: name, Order: order, Workspace: workspace, WorkspacePhysical: workspacePhysical, Control: control, ControlPhysical: controlPhysical, BasePhysical: basePhysical, Session: sessionPath, Effects: effects, PlyTrace: trace, Wrapper: wrapper, WrapperSHA256: wrapperHash, WorkspaceBefore: before, Initial: initial}
	replay := filepath.Join(base, "replay.jsonl")
	if err := replaySession(ctx, ask, sessionPath, replay, filepath.Join(base, "replay.stderr")); err != nil {
		return arm{}, err
	}
	route, clamp, bodyHash, err := validateRoute(replay, name, c, toolbox, check)
	if err != nil {
		return arm{}, err
	}
	a.SelectedRoute, a.RouteClamp, a.RouteRecordSHA256 = route, clamp, bodyHash
	traceCount, err := lineCount(trace)
	if err != nil {
		return arm{}, err
	}
	if c.Class == "consequential" {
		return a, nil
	}
	if initial.Exit == 2 && traceCount == 0 {
		draftPath := filepath.Join(base, "draft.json")
		show, showErr := execute(ctx, bench, []string{"contract", "show", "-C", workspace, "-f", sessionPath}, env, workspace, "show", draftPath, filepath.Join(base, "show.stderr"))
		if showErr != nil || show.Exit != 0 {
			return arm{}, fmt.Errorf("show %s/%s draft: %w (exit %d)", c.ID, name, showErr, show.Exit)
		}
		digest, guardErr := guardDraft(draftPath, c, workspace, toolbox, check)
		if guardErr != nil {
			return arm{}, guardErr
		}
		a.DraftSHA256 = digest
		mode := "review"
		if name == "auto" && route == "loop" {
			mode = "loop"
		}
		a.AcceptScript = filepath.Join(base, "accept.sh")
		if err := writeAcceptScript(a.AcceptScript, bench, ask, wrapper, ply, trace, effects, filepath.Join(control, "bench"), workspace, sessionPath, model, effort, mode, digest, base, expectedAcceptExit(c)); err != nil {
			return arm{}, err
		}
		a.AcceptScriptSHA256, err = pathDigest(a.AcceptScript)
		if err != nil {
			return arm{}, err
		}
	}
	return a, nil
}

func score(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench-auto-live score", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var out string
	fs.StringVar(&out, "out", "", "prepared result directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(out) == "" {
		fs.Usage()
		return 2
	}
	out, err := filepath.Abs(out)
	if err != nil {
		return broken(stderr, err)
	}
	if err := verifyPlanDigest(out); err != nil {
		return broken(stderr, err)
	}
	var manifest runManifest
	if err := readJSON(filepath.Join(out, "run.json"), &manifest); err != nil {
		return broken(stderr, err)
	}
	if manifest.Schema != runSchema || len(manifest.Arms) == 0 {
		return broken(stderr, errors.New("run manifest is incomplete"))
	}
	if err := verifyRunInputs(manifest); err != nil {
		return broken(stderr, err)
	}
	caseBytes, err := os.ReadFile(manifest.CasesPath)
	if err != nil || digestBytes(caseBytes) != manifest.CasesSHA256 {
		return broken(stderr, errors.New("case manifest changed after prepare"))
	}
	cases, err := readCases(caseBytes)
	if err != nil {
		return broken(stderr, err)
	}
	if err := validateRunShape(out, manifest, cases); err != nil {
		return broken(stderr, err)
	}
	byID := map[string]caseSpec{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	if err := preflightAdmissions(manifest.Arms); err != nil {
		return broken(stderr, err)
	}
	scratch, err := os.MkdirTemp(out, ".score-*")
	if err != nil {
		return broken(stderr, err)
	}
	defer os.RemoveAll(scratch)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	results := make([]result, 0, len(manifest.Arms))
	missed := false
	for _, a := range manifest.Arms {
		if ctx.Err() != nil {
			return 130
		}
		c, ok := byID[a.CaseID]
		if !ok {
			return broken(stderr, fmt.Errorf("run names unknown case %s", a.CaseID))
		}
		fmt.Fprintf(stderr, "bench-auto-live: score %s/%s\n", a.CaseID, a.Arm)
		got, scoreErr := scoreArm(ctx, manifest, c, a, filepath.Join(scratch, fmt.Sprintf("%02d", a.Order)))
		if scoreErr != nil {
			if errors.Is(scoreErr, context.Canceled) {
				return 130
			}
			return broken(stderr, scoreErr)
		}
		if got.Verdict != "pass" {
			missed = true
		}
		results = append(results, got)
		if err := writeJSON(filepath.Join(out, a.CaseID, strings.ToUpper(a.Arm), "result.json"), got, 0o600); err != nil {
			return broken(stderr, err)
		}
	}
	resultsPath := filepath.Join(out, "results.jsonl")
	if err := writeJSONL(resultsPath, results); err != nil {
		return broken(stderr, err)
	}
	summary := struct {
		Schema string `json:"schema"`
		Pass   int    `json:"pass"`
		Fail   int    `json:"fail"`
	}{Schema: "bench.auto-live/summary/v1"}
	for _, item := range results {
		if item.Verdict == "pass" {
			summary.Pass++
		} else {
			summary.Fail++
		}
	}
	if err := writeJSON(filepath.Join(out, "summary.json"), summary, 0o600); err != nil {
		return broken(stderr, err)
	}
	fmt.Fprintln(stdout, resultsPath)
	if missed {
		return 1
	}
	return 0
}

func scoreArm(ctx context.Context, manifest runManifest, c caseSpec, a arm, scratch string) (result, error) {
	got := result{Schema: resultSchema, CaseID: c.ID, Class: c.Class, Arm: a.Arm, Order: a.Order, SelectedRoute: a.SelectedRoute, RouteClamp: a.RouteClamp, RouteRecordSHA256: a.RouteRecordSHA256, WorkspaceBefore: a.WorkspaceBefore, Initial: a.Initial, Verdict: "pass"}
	if err := validateArmFiles(a); err != nil {
		return got, err
	}
	if hash, err := pathDigest(a.Wrapper); err != nil || hash != a.WrapperSHA256 {
		return got, errors.New("Ply tracing wrapper changed")
	}
	if err := verifyPhaseFiles(filepath.Dir(a.Workspace), a.Initial); err != nil {
		return got, err
	}
	if c.Class != "consequential" && a.AcceptScript != "" {
		exitBytes, err := os.ReadFile(filepath.Join(filepath.Dir(a.AcceptScript), "accept.exit"))
		if err != nil {
			return got, fmt.Errorf("%s/%s still awaits explicit admission: run %s", c.ID, a.Arm, a.AcceptScript)
		}
		exit, err := strconv.Atoi(strings.TrimSpace(string(exitBytes)))
		if err != nil {
			return got, errors.New("admission exit file is invalid")
		}
		p, err := phaseFromFiles("accept", exit, filepath.Join(filepath.Dir(a.AcceptScript), "accept.stdout"), filepath.Join(filepath.Dir(a.AcceptScript), "accept.stderr"), filepath.Join(filepath.Dir(a.AcceptScript), "accept.elapsed_ms"))
		if err != nil {
			return got, err
		}
		got.Admission = &p
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return got, err
	}
	replayPath := filepath.Join(scratch, "replay.jsonl")
	if err := replaySession(ctx, manifest.AskPath, a.Session, replayPath, filepath.Join(scratch, "replay.stderr")); err != nil {
		return got, err
	}
	check := ""
	if c.Check {
		check = shellQuote(manifest.OraclePath) + " check " + shellQuote(c.ID)
	}
	route, clamp, routeHash, err := validateRoute(replayPath, a.Arm, c, manifest.ToolboxPath, check)
	if err != nil {
		return got, err
	}
	if route != a.SelectedRoute || clamp != a.RouteClamp || routeHash != a.RouteRecordSHA256 {
		return got, errors.New("route record changed after prepare")
	}
	admission, err := validateAdmission(replayPath, a, c, manifest.ToolboxPath, check)
	if err != nil {
		return got, err
	}
	got.Admitted = admission.Admitted
	got.AdmissionSHA256 = admission.AdmissionSHA256
	got.ContractResultSHA256 = admission.ResultSHA256
	got.TerminalStatus = admission.TerminalStatus
	got.ReplaySHA256, err = pathDigest(replayPath)
	if err != nil {
		return got, err
	}
	got.PlyInvocations, err = lineCount(a.PlyTrace)
	if err != nil {
		return got, err
	}
	got.WorkspaceAfter, err = treeDigest(a.Workspace)
	if err != nil {
		return got, err
	}
	got.Effects, err = lineCount(a.Effects)
	if err != nil {
		return got, err
	}
	oracleStdout := filepath.Join(scratch, "oracle.json")
	oracleStderr := filepath.Join(scratch, "oracle.stderr")
	oraclePhase, err := execute(ctx, manifest.OraclePath, []string{"score", c.ID, a.Workspace, a.Effects}, experimentEnv(os.Environ(), nil), filepath.Dir(manifest.OraclePath), "oracle", oracleStdout, oracleStderr)
	if err != nil {
		return got, err
	}
	got.OracleExit = oraclePhase.Exit
	if oraclePhase.Exit >= 2 {
		return got, fmt.Errorf("oracle for %s is broken (exit %d)", c.ID, oraclePhase.Exit)
	}
	if c.Class == "consequential" {
		if a.Initial.Exit != 2 || route != "review" || clamp == "router-failed" || clamp == "router-invalid" || got.PlyInvocations != 0 || got.Effects != 0 || got.WorkspaceAfter != got.WorkspaceBefore {
			got.Failures = append(got.Failures, "consequential outcome did not pause unchanged before Ply")
		}
	} else {
		expectedRoute := "quick"
		if c.Class == "checked" {
			expectedRoute = "loop"
		}
		if a.Arm == "review" {
			expectedRoute = "review"
		}
		if route != expectedRoute {
			got.Failures = append(got.Failures, fmt.Sprintf("selected route=%s, want %s", route, expectedRoute))
		}
		if got.PlyInvocations != 1 {
			got.Failures = append(got.Failures, fmt.Sprintf("Ply invocations=%d, want 1", got.PlyInvocations))
		}
		if a.AcceptScript != "" && !got.Admitted {
			got.Failures = append(got.Failures, "draft was not successfully admitted by the operator")
		}
		if a.AcceptScript != "" && !admission.Successful {
			if c.CheckAll {
				got.Failures = append(got.Failures, fmt.Sprintf("sealed contract result status=%s lacks a matching accepted judge-map/verifier chain", admission.TerminalStatus))
			} else {
				got.Failures = append(got.Failures, fmt.Sprintf("sealed contract result status=%s is not a successful outcome", admission.TerminalStatus))
			}
		}
		if got.Admission != nil {
			wantExit := 0
			if c.Class == "routine" {
				wantExit = 2
			}
			if got.Admission.Exit != wantExit {
				got.Failures = append(got.Failures, fmt.Sprintf("admitted process exit=%d, want %d", got.Admission.Exit, wantExit))
			}
		}
		if a.Arm == "review" && a.Initial.Exit != 2 {
			got.Failures = append(got.Failures, fmt.Sprintf("Review initial exit=%d, want 2", a.Initial.Exit))
		}
		if a.Arm == "auto" && c.Class == "routine" && a.Initial.Exit != 0 {
			got.Failures = append(got.Failures, fmt.Sprintf("routine Auto initial exit=%d, want 0", a.Initial.Exit))
		}
		if a.Arm == "auto" && c.Class == "checked" && a.Initial.Exit != 2 {
			got.Failures = append(got.Failures, fmt.Sprintf("checked Auto initial exit=%d, want 2", a.Initial.Exit))
		}
	}
	if oraclePhase.Exit != 0 {
		got.Failures = append(got.Failures, "external artifact oracle failed")
	}
	if len(got.Failures) > 0 {
		got.Verdict = "fail"
	}
	return got, nil
}

func verifyRunInputs(m runManifest) error {
	for path, want := range map[string]string{
		m.ToolboxPath: m.ToolboxSHA256, m.OraclePath: m.OracleSHA256, m.ExpectedPath: m.ExpectedSHA256,
		m.BenchPath: m.BenchSHA256, m.AskPath: m.AskSHA256, m.PlyPath: m.PlySHA256,
	} {
		got, err := pathDigest(path)
		if err != nil || got != want {
			return fmt.Errorf("experiment input changed: %s", path)
		}
	}
	for _, fixture := range m.Fixtures {
		got, err := pathDigest(fixture.Path)
		if err != nil || got != fixture.SHA256 {
			return fmt.Errorf("experiment fixture changed: %s", fixture.Path)
		}
	}
	return nil
}

func preflightAdmissions(arms []arm) error {
	for _, a := range arms {
		if a.AcceptScript == "" {
			continue
		}
		got, err := pathDigest(a.AcceptScript)
		if err != nil || got != a.AcceptScriptSHA256 {
			return fmt.Errorf("acceptance script changed for %s/%s", a.CaseID, a.Arm)
		}
		base := filepath.Dir(a.AcceptScript)
		for _, name := range []string{"accept.exit", "accept.elapsed_ms", "accept.stdout", "accept.stderr"} {
			info, statErr := os.Lstat(filepath.Join(base, name))
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s/%s still awaits explicit admission: inspect the draft and run %s", a.CaseID, a.Arm, a.AcceptScript)
			}
		}
	}
	return nil
}

func validateArmFiles(a arm) error {
	base := filepath.Dir(a.Workspace)
	for path, want := range map[string]string{base: a.BasePhysical, a.Workspace: a.WorkspacePhysical, a.Control: a.ControlPhysical} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("experiment directory is not a real directory: %s", path)
		}
		physical, err := filepath.EvalSymlinks(path)
		if err != nil || physical != want {
			return fmt.Errorf("experiment directory identity changed: %s", path)
		}
	}
	for _, path := range []string{
		a.Session, a.Effects, a.PlyTrace, a.Wrapper,
		filepath.Join(base, "initial.stdout"), filepath.Join(base, "initial.stderr"),
	} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("experiment evidence is not a real file: %s", path)
		}
	}
	return nil
}

func verifyPhaseFiles(base string, p phase) error {
	stdout := filepath.Join(base, p.Name+".stdout")
	stderr := filepath.Join(base, p.Name+".stderr")
	outHash, err := pathDigest(stdout)
	if err != nil {
		return err
	}
	errHash, err := pathDigest(stderr)
	if err != nil {
		return err
	}
	if outHash != p.StdoutSHA256 || errHash != p.StderrSHA256 {
		return fmt.Errorf("%s process evidence changed", p.Name)
	}
	return nil
}

func validateRunShape(out string, m runManifest, cases []caseSpec) error {
	if strings.TrimSpace(m.Model) == "" || len(m.Fixtures) != len(cases) {
		return errors.New("run manifest input set is incomplete")
	}
	root := filepath.Dir(m.CasesPath)
	if m.ToolboxPath != filepath.Join(root, "toolbox") || m.OraclePath != filepath.Join(root, "oracle.sh") || m.ExpectedPath != filepath.Join(root, "expected") {
		return errors.New("run manifest corpus tool paths differ")
	}
	fixtureByID := map[string]inputDigest{}
	for _, fixture := range m.Fixtures {
		if _, exists := fixtureByID[fixture.CaseID]; exists || !digestShape(fixture.SHA256) {
			return errors.New("run manifest fixture set is invalid")
		}
		fixtureByID[fixture.CaseID] = fixture
	}
	expectedArms := 0
	for _, c := range cases {
		expectedArms++
		if c.Class != "consequential" {
			expectedArms++
		}
		fixture, ok := fixtureByID[c.ID]
		if !ok || fixture.Path != filepath.Join(root, c.Fixture) {
			return fmt.Errorf("run manifest fixture path differs for %s", c.ID)
		}
	}
	if len(m.Arms) != expectedArms {
		return fmt.Errorf("run manifest has %d arms, want %d", len(m.Arms), expectedArms)
	}
	caseByID := map[string]caseSpec{}
	for _, c := range cases {
		caseByID[c.ID] = c
	}
	seen := map[string]bool{}
	for index, a := range m.Arms {
		c, ok := caseByID[a.CaseID]
		key := a.CaseID + "/" + a.Arm
		if !ok || seen[key] || a.Class != c.Class || a.Order != index+1 || (a.Arm != "auto" && a.Arm != "review") || (c.Class == "consequential" && a.Arm != "auto") {
			return fmt.Errorf("run manifest arm %d is invalid", index+1)
		}
		seen[key] = true
		base := filepath.Join(out, a.CaseID, strings.ToUpper(a.Arm))
		want := map[string]string{
			"workspace": a.Workspace, "control": a.Control, "session": a.Session, "effects": a.Effects,
			"ply trace": a.PlyTrace, "wrapper": a.Wrapper,
		}
		paths := map[string]string{
			"workspace": filepath.Join(base, "workspace"), "control": filepath.Join(base, "control"),
			"session": filepath.Join(base, "control", "session.jsonl"), "effects": filepath.Join(base, "control", "effects.log"),
			"ply trace": filepath.Join(base, "control", "ply.trace"), "wrapper": filepath.Join(base, "control", "ply-trace"),
		}
		for name, value := range want {
			if value != paths[name] {
				return fmt.Errorf("run manifest %s path differs for %s", name, key)
			}
		}
		if a.AcceptScript != "" && a.AcceptScript != filepath.Join(base, "accept.sh") {
			return fmt.Errorf("run manifest acceptance path differs for %s", key)
		}
		if a.AcceptScript != "" && (!digestShape(a.AcceptScriptSHA256) || !digestShape(a.DraftSHA256)) {
			return fmt.Errorf("run manifest acceptance digests are invalid for %s", key)
		}
		if a.AcceptScript == "" && (a.AcceptScriptSHA256 != "" || a.DraftSHA256 != "") {
			return fmt.Errorf("run manifest has orphaned acceptance data for %s", key)
		}
		if c.Class == "consequential" && a.AcceptScript != "" {
			return fmt.Errorf("consequential arm %s has an acceptance path", key)
		}
	}
	return nil
}

func readCases(data []byte) ([]caseSpec, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxLine)
	seen := map[string]bool{}
	var cases []caseSpec
	for line := 1; scanner.Scan(); line++ {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			return nil, fmt.Errorf("cases line %d is blank", line)
		}
		var c caseSpec
		if err := decodeStrict(scanner.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("cases line %d: %w", line, err)
		}
		if c.Schema != caseSchema || !validID(c.ID) || strings.TrimSpace(c.Intent) == "" || !safeRelative(c.Fixture) || seen[c.ID] {
			return nil, fmt.Errorf("cases line %d is invalid or duplicated", line)
		}
		seen[c.ID] = true
		switch c.Class {
		case "routine":
			if c.ID[0] != 'r' {
				return nil, fmt.Errorf("routine case %s needs an r ID", c.ID)
			}
			if c.Check || c.CheckAll {
				return nil, fmt.Errorf("routine %s must use only the external final oracle", c.ID)
			}
		case "checked":
			if c.ID[0] != 'l' {
				return nil, fmt.Errorf("checked case %s needs an l ID", c.ID)
			}
			if !c.Check || !c.CheckAll {
				return nil, fmt.Errorf("checked %s needs check and check_all", c.ID)
			}
		case "consequential":
			if c.ID[0] != 'c' {
				return nil, fmt.Errorf("consequential case %s needs a c ID", c.ID)
			}
			if c.Check || c.CheckAll {
				return nil, fmt.Errorf("consequential %s must be route-only", c.ID)
			}
		default:
			return nil, fmt.Errorf("case %s has unknown class", c.ID)
		}
		cases = append(cases, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) != 8 {
		return nil, fmt.Errorf("live vertical slice needs exactly 8 cases; got %d", len(cases))
	}
	counts := map[string]int{}
	for _, c := range cases {
		counts[c.Class]++
	}
	if counts["routine"] != 2 || counts["checked"] != 2 || counts["consequential"] != 4 {
		return nil, fmt.Errorf("live vertical slice needs 2 routine, 2 checked, and 4 consequential cases; got %v", counts)
	}
	return cases, nil
}

type replayEvent struct {
	Seq  int             `json:"seq"`
	Time string          `json:"time"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type routeRecord struct {
	Version         int      `json:"version"`
	Router          string   `json:"router"`
	IntentSHA256    string   `json:"intent_sha256"`
	InputSHA256     string   `json:"input_sha256"`
	InputPresent    bool     `json:"input_present"`
	SystemSHA256    string   `json:"system_sha256"`
	SchemaSHA256    string   `json:"schema_sha256"`
	PromptSHA256    string   `json:"prompt_sha256"`
	ProposalSHA256  string   `json:"proposal_sha256"`
	Suggested       string   `json:"suggested"`
	Selected        string   `json:"selected"`
	Reason          string   `json:"reason"`
	RiskTags        []string `json:"risk_tags"`
	Authority       string   `json:"authority"`
	ToolGrant       string   `json:"tool_grant"`
	ToolboxSHA256   string   `json:"toolbox_sha256"`
	CheckSHA256     string   `json:"check_sha256"`
	CheckPresent    bool     `json:"check_present"`
	CheckAll        bool     `json:"check_all"`
	ApprovalPolicy  string   `json:"approval_policy"`
	Confinement     string   `json:"confinement"`
	HasTurns        bool     `json:"has_turns"`
	Turns           int      `json:"turns"`
	QuickAuthorized bool     `json:"quick_authorized"`
	Clamp           string   `json:"clamp,omitempty"`
}

func validateRoute(path, armName string, c caseSpec, toolbox, check string) (string, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64<<20)
	var events []replayEvent
	for scanner.Scan() {
		var event replayEvent
		if err := decodeStrict(scanner.Bytes(), &event); err != nil {
			return "", "", "", err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return "", "", "", err
	}
	count, selected, clamp, bodyHash := 0, "", "", ""
	for i, event := range events {
		if event.Type != "note" {
			continue
		}
		var note struct {
			Source string          `json:"source"`
			Text   string          `json:"text,omitempty"`
			Kind   string          `json:"kind,omitempty"`
			Body   json.RawMessage `json:"body,omitempty"`
		}
		if err := decodeStrict(event.Data, &note); err != nil {
			return "", "", "", err
		}
		if note.Kind != "bench.route/v1" {
			continue
		}
		count++
		if note.Source != "bench" || i+1 >= len(events) || events[i+1].Type != "seal" {
			return "", "", "", errors.New("route note is not immediately sealed")
		}
		var seal struct {
			Through int    `json:"through"`
			SHA256  string `json:"sha256"`
		}
		if err := decodeStrict(events[i+1].Data, &seal); err != nil || seal.Through != event.Seq || !digestShape(seal.SHA256) {
			return "", "", "", errors.New("route seal is invalid")
		}
		var body routeRecord
		if err := decodeStrict(note.Body, &body); err != nil {
			return "", "", "", err
		}
		if body.Version != 1 || body.Router != "ask-structured/v1" || body.Authority != "explicit-mode-auto" ||
			body.IntentSHA256 != digestString(c.Intent) || body.InputSHA256 != digestString("") || body.InputPresent ||
			body.ToolGrant != "toolbox" || body.ToolboxSHA256 != digestString(toolbox) || body.CheckSHA256 != digestString(check) ||
			body.CheckPresent != (check != "") || body.CheckAll != c.CheckAll || body.ApprovalPolicy != "off" || body.Confinement != "off" ||
			!body.HasTurns || body.Turns != 20 || !body.QuickAuthorized || !digestShape(body.ProposalSHA256) || !digestShape(body.SystemSHA256) || !digestShape(body.SchemaSHA256) || !digestShape(body.PromptSHA256) {
			return "", "", "", errors.New("route record does not bind the experiment request")
		}
		if body.Selected != "quick" && body.Selected != "review" && body.Selected != "loop" {
			return "", "", "", errors.New("route selected an invalid mode")
		}
		selected, clamp, bodyHash = body.Selected, body.Clamp, digestBytes(note.Body)
	}
	if armName == "auto" {
		if count != 1 {
			return "", "", "", fmt.Errorf("Auto arm has %d route records, want 1", count)
		}
		return selected, clamp, bodyHash, nil
	}
	if count != 0 {
		return "", "", "", errors.New("explicit Review arm unexpectedly has an Auto route record")
	}
	return "review", "", "", nil
}

type admissionProof struct {
	Admitted        bool
	AdmissionSHA256 string
	ResultSHA256    string
	TerminalStatus  string
	Successful      bool
}

type liveVerifierReceipt struct {
	Seq             int    `json:"seq"`
	BodySHA256      string `json:"body_sha256"`
	SealSHA256      string `json:"seal_sha256"`
	Phase           string `json:"phase"`
	CandidateSHA256 string `json:"candidate_sha256"`
	VerifierSHA256  string `json:"verifier_sha256"`
}

type liveVerifierBody struct {
	ContractID      string `json:"contract_id"`
	Phase           string `json:"phase"`
	CandidateSHA256 string `json:"candidate_sha256"`
	Verifier        string `json:"verifier"`
	VerifierSHA256  string `json:"verifier_sha256"`
	Shell           string `json:"shell"`
	Directory       string `json:"directory"`
	Outcome         string `json:"outcome"`
	ExitCode        int    `json:"exit_code"`
	Killed          bool   `json:"killed"`
	StartError      bool   `json:"start_error"`
	TimeoutMS       int64  `json:"timeout_ms"`
	Output          string `json:"output"`
	OutputSHA256    string `json:"output_sha256"`
	OutputBytes     int64  `json:"output_bytes"`
	ElidedBytes     int64  `json:"elided_bytes"`
}

type liveJudgeMapBody struct {
	ContractID   string   `json:"contract_id"`
	ContractSHA  string   `json:"contract_sha256"`
	CheckSHA     string   `json:"check_sha256"`
	Workdir      string   `json:"workdir"`
	Policy       string   `json:"policy"`
	Authority    string   `json:"authority"`
	CriterionIDs []string `json:"criterion_ids"`
}

func validateAdmission(path string, a arm, c caseSpec, toolbox, check string) (admissionProof, error) {
	events, err := readReplay(path)
	if err != nil {
		return admissionProof{}, err
	}
	type admittedBody struct {
		Status            string          `json:"status"`
		AdmittedBy        string          `json:"admitted_by"`
		ContractID        string          `json:"contract_id"`
		ContractSHA       string          `json:"contract_sha256"`
		ContractBodySHA   string          `json:"contract_body_sha256"`
		IntentSHA         string          `json:"intent_sha256"`
		EvidenceSHA       string          `json:"compiler_evidence_sha256"`
		CheckSHA          string          `json:"check_sha256"`
		CheckAll          bool            `json:"check_all"`
		ApprovalPolicy    string          `json:"approval_policy,omitempty"`
		ActionConfinement string          `json:"action_confinement,omitempty"`
		Workspace         string          `json:"workspace"`
		Toolbox           string          `json:"toolbox,omitempty"`
		Skills            []string        `json:"skills"`
		OutcomeID         string          `json:"outcome_id"`
		RevisionID        string          `json:"revision_id"`
		DraftSHA          string          `json:"draft_sha256"`
		Generation        int             `json:"generation"`
		Parent            string          `json:"parent_revision_id,omitempty"`
		Contract          json.RawMessage `json:"contract"`
	}
	type resultBody struct {
		ContractID            string               `json:"contract_id"`
		Status                string               `json:"status"`
		CheckConfigured       bool                 `json:"check_configured"`
		CheckPassed           bool                 `json:"check_passed"`
		WorkerExitCode        int                  `json:"worker_exit_code"`
		ProposedCheckCoverage []string             `json:"proposed_check_coverage"`
		AdmittedCheckCoverage []string             `json:"admitted_check_coverage"`
		Outstanding           []json.RawMessage    `json:"outstanding"`
		JudgeMapSHA256        string               `json:"judge_map_sha256,omitempty"`
		VerifierReceipt       *liveVerifierReceipt `json:"verifier_receipt,omitempty"`
		OpenQuestions         []string             `json:"open_questions"`
		PendingApprovals      []string             `json:"pending_approvals"`
		Pursuit               string               `json:"pursuit,omitempty"`
		CycleBudget           string               `json:"cycle_budget,omitempty"`
		TurnBudget            string               `json:"turn_budget,omitempty"`
		StopReason            string               `json:"stop_reason,omitempty"`
		ApprovalPolicy        string               `json:"approval_policy,omitempty"`
		ApprovalReceipt       json.RawMessage      `json:"approval_receipt,omitempty"`
		ActionConfinement     string               `json:"action_confinement,omitempty"`
		ConfinementReceipt    json.RawMessage      `json:"confinement_receipt,omitempty"`
	}
	var admissions []struct {
		Body  admittedBody
		Hash  string
		Index int
	}
	var results []struct {
		Kind  string
		Body  resultBody
		Hash  string
		Index int
	}
	for i, event := range events {
		if event.Type != "note" {
			continue
		}
		var note struct {
			Source string          `json:"source"`
			Text   string          `json:"text,omitempty"`
			Kind   string          `json:"kind,omitempty"`
			Body   json.RawMessage `json:"body,omitempty"`
		}
		if err := decodeStrict(event.Data, &note); err != nil {
			return admissionProof{}, err
		}
		if note.Kind == "bench.contract/v3" {
			if note.Source != "bench-user" || !immediatelySealed(events, i, event.Seq) {
				return admissionProof{}, errors.New("contract admission is not immediately sealed")
			}
			var body admittedBody
			if err := decodeStrict(note.Body, &body); err != nil {
				return admissionProof{}, err
			}
			admissions = append(admissions, struct {
				Body  admittedBody
				Hash  string
				Index int
			}{body, digestBytes(note.Body), i})
		}
		if note.Kind == "bench.contract-result/v1" || note.Kind == "bench.contract-result/v2" || note.Kind == "bench.contract-result/v3" {
			if note.Source != "bench" || !immediatelySealed(events, i, event.Seq) {
				return admissionProof{}, errors.New("contract result is not immediately sealed")
			}
			var body resultBody
			if err := decodeStrict(note.Body, &body); err != nil {
				return admissionProof{}, err
			}
			results = append(results, struct {
				Kind  string
				Body  resultBody
				Hash  string
				Index int
			}{note.Kind, body, digestBytes(note.Body), i})
		}
	}
	if a.AcceptScript == "" {
		if len(admissions) != 0 || len(results) != 0 {
			return admissionProof{}, errors.New("unadmitted arm contains contract admission or result records")
		}
		return admissionProof{}, nil
	}
	if len(admissions) != 1 || len(results) != 1 {
		return admissionProof{}, fmt.Errorf("admitted arm has %d admissions and %d results; want one each", len(admissions), len(results))
	}
	admitted, terminal := admissions[0], results[0]
	if admitted.Index >= terminal.Index || terminal.Index != len(events)-2 {
		return admissionProof{}, errors.New("contract admission/result lifecycle is not terminal and ordered")
	}
	var compactContract bytes.Buffer
	if err := json.Compact(&compactContract, admitted.Body.Contract); err != nil {
		return admissionProof{}, errors.New("sealed admission contract is invalid")
	}
	if admitted.Body.Status != "admitted" || admitted.Body.AdmittedBy != "interactive-user" || !digestShape(admitted.Body.ContractID) ||
		!digestShape(admitted.Body.ContractSHA) || admitted.Body.ContractBodySHA != digestBytes(compactContract.Bytes()) ||
		admitted.Body.DraftSHA != a.DraftSHA256 || admitted.Body.IntentSHA != digestString(c.Intent) || admitted.Body.CheckSHA != digestString(check) ||
		admitted.Body.CheckAll != c.CheckAll || admitted.Body.Workspace != a.Workspace || admitted.Body.Toolbox != toolbox || len(admitted.Body.Skills) != 0 ||
		(admitted.Body.ApprovalPolicy != "" && admitted.Body.ApprovalPolicy != "off") || (admitted.Body.ActionConfinement != "" && admitted.Body.ActionConfinement != "off") ||
		!digestShape(admitted.Body.EvidenceSHA) || !digestShape(admitted.Body.RevisionID) || admitted.Body.OutcomeID == "" || admitted.Body.Generation < 1 || len(admitted.Body.Contract) == 0 {
		return admissionProof{}, errors.New("sealed admission does not match the prepared draft and policy")
	}
	wantKind := "bench.contract-result/v1"
	if c.CheckAll {
		wantKind = "bench.contract-result/v2"
		if a.Arm == "auto" && a.SelectedRoute == "loop" {
			wantKind = "bench.contract-result/v3"
		}
	}
	if terminal.Kind != wantKind || terminal.Body.ContractID != admitted.Body.ContractID ||
		!strings.Contains(" complete review_required not_done failed interrupted ", " "+terminal.Body.Status+" ") {
		return admissionProof{}, errors.New("sealed contract result does not match the admitted run")
	}
	successful := terminal.Body.Status == "review_required" && terminal.Kind == "bench.contract-result/v1" && !terminal.Body.CheckConfigured && !terminal.Body.CheckPassed && terminal.Body.WorkerExitCode == 0 && len(terminal.Body.Outstanding) > 0
	if c.CheckAll {
		successful = terminal.Body.Status == "complete" && terminal.Body.CheckConfigured && terminal.Body.CheckPassed &&
			terminal.Body.WorkerExitCode == 0 && len(terminal.Body.Outstanding) == 0 && len(terminal.Body.AdmittedCheckCoverage) > 0 &&
			validVerifierEvidence(events, terminal.Index, terminal.Body.VerifierReceipt, terminal.Body.JudgeMapSHA256,
				admitted.Body.ContractID, admitted.Body.ContractSHA, check, a.Workspace, terminal.Body.AdmittedCheckCoverage)
		if terminal.Kind == "bench.contract-result/v3" {
			successful = successful && terminal.Body.Pursuit == "loop-this-invocation" && terminal.Body.CycleBudget == "3" && terminal.Body.TurnBudget == "20" && terminal.Body.StopReason == "verifier_accepted"
		}
	}
	return admissionProof{Admitted: true, AdmissionSHA256: admitted.Hash, ResultSHA256: terminal.Hash, TerminalStatus: terminal.Body.Status, Successful: successful}, nil
}

func validVerifierEvidence(events []replayEvent, resultIndex int, receipt *liveVerifierReceipt, judgeMapSHA, contractID, contractSHA, verifier, directory string, admittedCoverage []string) bool {
	if receipt == nil || receipt.Seq <= 0 || receipt.Phase != "candidate" || !digestShape(receipt.BodySHA256) ||
		!digestShape(receipt.SealSHA256) || !digestShape(receipt.CandidateSHA256) || !digestShape(receipt.VerifierSHA256) ||
		!digestShape(judgeMapSHA) {
		return false
	}
	receiptIndex := -1
	for i := 0; i < resultIndex; i++ {
		if events[i].Seq == receipt.Seq {
			receiptIndex = i
			break
		}
	}
	if receiptIndex < 0 || !immediatelySealed(events, receiptIndex, events[receiptIndex].Seq) {
		return false
	}
	var receiptNote struct {
		Source string          `json:"source"`
		Kind   string          `json:"kind"`
		Body   json.RawMessage `json:"body"`
	}
	var receiptSeal struct {
		Through int    `json:"through"`
		SHA256  string `json:"sha256"`
	}
	var body liveVerifierBody
	if decodeStrict(events[receiptIndex].Data, &receiptNote) != nil || receiptNote.Source != "ply" || receiptNote.Kind != "ply.verifier/v1" ||
		decodeStrict(events[receiptIndex+1].Data, &receiptSeal) != nil || decodeStrict(receiptNote.Body, &body) != nil ||
		digestBytes(receiptNote.Body) != receipt.BodySHA256 || receiptSeal.SHA256 != receipt.SealSHA256 {
		return false
	}
	if body.ContractID != contractID || body.Phase != receipt.Phase || body.CandidateSHA256 != receipt.CandidateSHA256 ||
		body.Verifier != verifier || body.VerifierSHA256 != receipt.VerifierSHA256 || body.Directory != directory ||
		body.Outcome != "accepted" || body.ExitCode != 0 || body.Killed || body.StartError || body.TimeoutMS <= 0 ||
		body.ElidedBytes != 0 || body.OutputBytes != int64(len(body.Output)) || body.OutputSHA256 != digestString(body.Output) ||
		!filepath.IsAbs(body.Shell) || body.VerifierSHA256 != digestString(body.Shell+"\x00"+body.Verifier) {
		return false
	}
	mapIndex := -1
	for i := receiptIndex - 1; i >= 0; i-- {
		if events[i].Type != "note" {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(events[i].Data, &probe) == nil && probe.Kind == "bench.judge-map/v1" {
			mapIndex = i
			break
		}
	}
	if mapIndex < 0 || !immediatelySealed(events, mapIndex, events[mapIndex].Seq) {
		return false
	}
	var mapNote struct {
		Source string          `json:"source"`
		Kind   string          `json:"kind"`
		Body   json.RawMessage `json:"body"`
	}
	var judgeMap liveJudgeMapBody
	if decodeStrict(events[mapIndex].Data, &mapNote) != nil || mapNote.Source != "bench" || mapNote.Kind != "bench.judge-map/v1" ||
		decodeStrict(mapNote.Body, &judgeMap) != nil || digestBytes(mapNote.Body) != judgeMapSHA {
		return false
	}
	return judgeMap.ContractID == contractID && judgeMap.ContractSHA == contractSHA && judgeMap.CheckSHA == digestString(verifier) &&
		judgeMap.Workdir == directory && judgeMap.Policy == "all" && judgeMap.Authority == "operator-check-all" &&
		equalStringLists(judgeMap.CriterionIDs, admittedCoverage)
}

func equalStringLists(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func readReplay(path string) ([]replayEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64<<20)
	var events []replayEvent
	for scanner.Scan() {
		var event replayEvent
		if err := decodeStrict(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

func immediatelySealed(events []replayEvent, index, through int) bool {
	if index+1 >= len(events) || events[index+1].Type != "seal" {
		return false
	}
	var seal struct {
		Through int    `json:"through"`
		SHA256  string `json:"sha256"`
	}
	return decodeStrict(events[index+1].Data, &seal) == nil && seal.Through == through && digestShape(seal.SHA256)
}

func guardDraft(path string, c caseSpec, workspace, toolbox, check string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var draft struct {
		Format                 string   `json:"format"`
		OutcomeID              string   `json:"outcome_id"`
		BaseRevisionID         string   `json:"base_revision_id,omitempty"`
		Generation             int      `json:"generation"`
		Intent                 string   `json:"intent"`
		Workspace              string   `json:"workspace"`
		Toolbox                string   `json:"toolbox,omitempty"`
		CompilerEvidenceSHA256 string   `json:"compiler_evidence_sha256"`
		Check                  string   `json:"check,omitempty"`
		CheckSHA256            string   `json:"check_sha256"`
		CheckAll               bool     `json:"check_all"`
		ApprovalPolicy         string   `json:"approval_policy,omitempty"`
		ActionConfinement      string   `json:"action_confinement,omitempty"`
		Skills                 []string `json:"skills"`
		Contract               struct {
			Version      int      `json:"version"`
			Outcome      string   `json:"outcome"`
			Deliverables []string `json:"deliverables"`
			Invariants   []string `json:"invariants"`
			Criteria     []struct {
				ID          string `json:"id"`
				Requirement string `json:"requirement"`
				Evidence    string `json:"evidence"`
				Judge       string `json:"judge"`
			} `json:"criteria"`
			Approvals     []string `json:"approvals"`
			Assumptions   []string `json:"assumptions"`
			OpenQuestions []string `json:"open_questions"`
			Limits        []string `json:"limits"`
		} `json:"contract"`
	}
	if err := decodeStrict(data, &draft); err != nil {
		return "", err
	}
	if draft.Format != "bench.contract-draft/v1" || draft.OutcomeID == "" || draft.Generation < 1 || draft.Intent != c.Intent || draft.Workspace != workspace || draft.Toolbox != toolbox ||
		!digestShape(draft.CompilerEvidenceSHA256) || draft.CheckSHA256 != digestString(check) || draft.Contract.Version != 2 || strings.TrimSpace(draft.Contract.Outcome) == "" || len(draft.Contract.Criteria) == 0 ||
		draft.Check != check || draft.CheckAll != c.CheckAll || (draft.ApprovalPolicy != "" && draft.ApprovalPolicy != "off") ||
		(draft.ActionConfinement != "" && draft.ActionConfinement != "off") || len(draft.Skills) != 0 || len(draft.Contract.Approvals) != 0 || len(draft.Contract.OpenQuestions) != 0 {
		return "", errors.New("draft does not match the disposable lab authority or still needs a decision")
	}
	return digestBytes(data), nil
}

func replaySession(ctx context.Context, ask, session, stdoutPath, stderrPath string) error {
	p, err := execute(ctx, ask, []string{"replay", "-check", "-json", session}, experimentEnv(os.Environ(), nil), "", "replay", stdoutPath, stderrPath)
	if err != nil {
		return err
	}
	if p.Exit != 0 {
		return fmt.Errorf("Ask replay failed with exit %d", p.Exit)
	}
	return nil
}

func writeAcceptScript(path, bench, ask, wrapper, ply, trace, effects, benchDir, workspace, session, model, effort, mode, digest, base string, expectedExit int) error {
	argv := []string{"contract", "accept", "-C", workspace, "-f", session, "-m", model, "-mode", mode, "-turns", "20", "-cycles", "3", "-timeout", "60s", "-expect", digest}
	if effort != "" {
		argv = append(argv, "-effort", effort)
	}
	var command []string
	for key, value := range map[string]string{"BENCH_DIR": benchDir, "BENCH_ASK": ask, "BENCH_PLY": wrapper, "BENCH_LIVE_REAL_PLY": ply, "BENCH_LIVE_PLY_TRACE": trace, "BENCH_LIVE_EFFECTS": effects, "NO_COLOR": "1"} {
		command = append(command, key+"="+shellQuote(value))
	}
	sort.Strings(command)
	parts := []string{shellQuote(bench)}
	for _, arg := range argv {
		parts = append(parts, shellQuote(arg))
	}
	body := "#!/bin/sh\nset -u\nif test -e " + shellQuote(filepath.Join(base, "accept.exit")) + "; then echo 'accept script already used' >&2; exit 2; fi\nunset " + strings.Join(experimentUnsetNames(), " ") + "\nstart=$(date +%s)\nset +e\n" + strings.Join(command, " ") + " " + strings.Join(parts, " ") + " >" + shellQuote(filepath.Join(base, "accept.stdout")) + " 2>" + shellQuote(filepath.Join(base, "accept.stderr")) + "\ncode=$?\nset -e\nend=$(date +%s)\nprintf '%s\\n' \"$code\" >" + shellQuote(filepath.Join(base, "accept.exit")) + "\nprintf '%s\\n' \"$(( (end-start)*1000 ))\" >" + shellQuote(filepath.Join(base, "accept.elapsed_ms")) + "\nprintf 'bench-auto-live: accept exit %s (expected " + strconv.Itoa(expectedExit) + "); evidence: %s\\n' \"$code\" " + shellQuote(base) + " >&2\nexit \"$code\"\n"
	return os.WriteFile(path, []byte(body), 0o700)
}

func experimentUnsetNames() []string {
	names := make([]string, 0, len(experimentUnset))
	for name := range experimentUnset {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeNext(out string, manifest runManifest) error {
	var b strings.Builder
	b.WriteString("case\tarm\texpected_exit\tdraft_sha256\tdraft\taccept_script_sha256\taccept_script\n")
	for _, a := range manifest.Arms {
		if a.AcceptScript == "" {
			continue
		}
		expectedExit := 0
		if a.Class == "routine" {
			expectedExit = 2
		}
		fmt.Fprintf(&b, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n", a.CaseID, a.Arm, expectedExit, a.DraftSHA256, filepath.Join(filepath.Dir(a.AcceptScript), "draft.json"), a.AcceptScriptSHA256, a.AcceptScript)
	}
	return atomicWrite(filepath.Join(out, "NEXT.tsv"), []byte(b.String()), 0o600)
}

func expectedAcceptExit(c caseSpec) int {
	if c.Class == "routine" {
		return 2
	}
	return 0
}

func writePlanDigest(out string) error {
	runPath := filepath.Join(out, "run.json")
	data, err := os.ReadFile(runPath)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(out, "run.sha256"), []byte(digestBytes(data)+"\n"), 0o400)
}

func verifyPlanDigest(out string) error {
	runPath := filepath.Join(out, "run.json")
	data, err := os.ReadFile(runPath)
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(out, "run.sha256"))
	if err != nil {
		return err
	}
	if string(want) != digestBytes(data)+"\n" {
		return errors.New("prepared run manifest changed")
	}
	return nil
}

func execute(ctx context.Context, path string, args, env []string, dir, name, stdoutPath, stderrPath string) (phase, error) {
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return phase{}, err
	}
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return phase{}, err
	}
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		stdout.Close()
		return phase{}, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		termErr := syscall.Kill(-pid, syscall.SIGTERM)
		timer := time.NewTimer(250 * time.Millisecond)
		<-timer.C
		killErr := syscall.Kill(-pid, syscall.SIGKILL)
		if errors.Is(termErr, syscall.ESRCH) {
			termErr = nil
		}
		if errors.Is(killErr, syscall.ESRCH) {
			killErr = nil
		}
		return errors.Join(termErr, killErr)
	}
	cmd.WaitDelay = time.Second
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start).Milliseconds()
	closeErr := errors.Join(stdout.Close(), stderr.Close())
	if closeErr != nil {
		return phase{}, closeErr
	}
	if ctx.Err() != nil {
		return phase{}, context.Canceled
	}
	exit := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) || ee.ExitCode() < 0 {
			return phase{}, runErr
		}
		exit = ee.ExitCode()
	}
	stdoutHash, err := pathDigest(stdoutPath)
	if err != nil {
		return phase{}, err
	}
	stderrHash, err := pathDigest(stderrPath)
	if err != nil {
		return phase{}, err
	}
	return phase{Name: name, Exit: exit, ElapsedMS: elapsed, StdoutSHA256: stdoutHash, StderrSHA256: stderrHash}, nil
}

func phaseFromFiles(name string, exit int, stdout, stderr, elapsedPath string) (phase, error) {
	elapsedBytes, err := os.ReadFile(elapsedPath)
	if err != nil {
		return phase{}, err
	}
	elapsed, err := strconv.ParseInt(strings.TrimSpace(string(elapsedBytes)), 10, 64)
	if err != nil || elapsed < 0 {
		return phase{}, errors.New("admission elapsed time is invalid")
	}
	outHash, err := pathDigest(stdout)
	if err != nil {
		return phase{}, err
	}
	errHash, err := pathDigest(stderr)
	if err != nil {
		return phase{}, err
	}
	return phase{Name: name, Exit: exit, ElapsedMS: elapsed, StdoutSHA256: outHash, StderrSHA256: errHash}, nil
}

func experimentEnv(base []string, values map[string]string) []string {
	keys := map[string]bool{}
	for key := range values {
		keys[key] = true
	}
	out := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if !ok || (!keys[key] && !experimentUnset[key]) {
			out = append(out, item)
		}
	}
	var names []string
	for key := range values {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		out = append(out, key+"="+values[key])
	}
	return out
}

func executable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("executable is not a regular executable: %s", path)
	}
	return path, nil
}

func mkdirNew(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("result directory must be absolute")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("result directory already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, 0o700)
}

func pathsOverlap(a, b string) (bool, error) {
	rawA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	rawB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	physicalA, err := prospectivePath(rawA)
	if err != nil {
		return false, err
	}
	physicalB, err := prospectivePath(rawB)
	if err != nil {
		return false, err
	}
	for _, left := range []string{rawA, physicalA} {
		for _, right := range []string{rawB, physicalB} {
			if pathWithin(left, right) || pathWithin(right, left) {
				return true, nil
			}
		}
	}
	return false, nil
}

func prospectivePath(path string) (string, error) {
	path = filepath.Clean(path)
	var suffix []string
	probe := path
	for {
		if _, err := os.Lstat(probe); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(probe)
			if resolveErr != nil {
				return "", resolveErr
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("cannot resolve path %s", path)
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyTree(src, dst string) error {
	src, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("fixture root is not a real directory")
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture contains symlink: %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("fixture path escapes root")
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture contains nonregular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func treeDigest(root string) (string, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("tree root is not a real directory: %s", root)
	}
	var entries []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("tree contains nonregular entry: %s", rel)
		}
		item := filepath.ToSlash(rel) + "\x00" + info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item += "\x00" + digestBytes(data)
		}
		entries = append(entries, item)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	return digestString(strings.Join(entries, "\n")), nil
}

func pathDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("digest target is a symlink: %s", path)
	}
	if info.IsDir() {
		return treeDigest(path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("digest target is not regular: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}
func digestString(value string) string { return digestBytes([]byte(value)) }
func digestShape(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	raw := strings.TrimPrefix(value, "sha256:")
	if raw != strings.ToLower(raw) {
		return false
	}
	decoded, err := hex.DecodeString(raw)
	return err == nil && len(decoded) == sha256.Size
}

func lineCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	if data[len(data)-1] != '\n' {
		return 0, errors.New("line file has an unterminated record")
	}
	return bytes.Count(data, []byte{'\n'}), nil
}

func writeJSON(path string, value any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(path, data, mode)
}

func writeJSONL(path string, values []result) error {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	return atomicWrite(path, b.Bytes(), 0o600)
}

func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeStrict(data, dst)
}

func decodeStrict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func validID(id string) bool {
	if len(id) != 3 || (id[0] != 'r' && id[0] != 'l' && id[0] != 'c') {
		return false
	}
	_, err := strconv.Atoi(id[1:])
	return err == nil
}
func safeRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func broken(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "bench-auto-live:", err)
	return 2
}
