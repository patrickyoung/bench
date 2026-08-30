package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	helperMode   = flag.String("bench-worker-helper", "", "internal bench-worker test helper mode")
	helperSource = flag.String("bench-worker-helper-source", "", "internal bench-worker test helper source")
	helperDest   = flag.String("bench-worker-helper-dest", "", "internal bench-worker test helper destination")
)

func TestWorkerHelperProcess(t *testing.T) {
	switch *helperMode {
	case "":
		return
	case "env":
		for _, entry := range os.Environ() {
			fmt.Println(entry)
		}
		os.Exit(0)
	case "stdin":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	case "literal":
		fmt.Fprintln(os.Stdout, strings.Join(flag.Args(), "|"))
		os.Exit(0)
	case "exit23":
		os.Exit(23)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "signal":
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
		os.Exit(72)
	case "hardlink":
		if err := os.Link(*helperSource, *helperDest); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(70)
		}
		os.Exit(0)
	default:
		os.Exit(71)
	}
}

type testLayout struct {
	root      string
	work      string
	inbox     string
	outbox    string
	inboxRun  string
	outboxRun string
	request   string
	receipt   string
	runID     string
	cfg       workerConfig
}

func newTestLayout(t *testing.T) testLayout {
	t.Helper()
	root := t.TempDir()
	layout := testLayout{
		root:   root,
		work:   filepath.Join(root, "work"),
		inbox:  filepath.Join(root, "run", "bench", "inbox"),
		outbox: filepath.Join(root, "run", "bench", "outbox"),
		runID:  "run-20260830_01",
	}
	layout.inboxRun = filepath.Join(layout.inbox, layout.runID)
	layout.outboxRun = filepath.Join(layout.outbox, layout.runID)
	layout.request = filepath.Join(layout.inboxRun, "request.json")
	layout.receipt = filepath.Join(layout.outboxRun, "receipt.json")
	for _, dir := range []string{layout.work, layout.inboxRun, layout.outboxRun} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	layout.cfg = workerConfig{workRoot: layout.work, inboxRoot: layout.inbox, outboxRoot: layout.outbox}
	return layout
}

func (layout testLayout) validRequest() workerRequest {
	return workerRequest{
		Schema:               requestSchema,
		RunID:                layout.runID,
		Argv:                 helperArgv("env"),
		CWD:                  layout.work,
		StdoutPath:           filepath.Join(layout.outboxRun, "stdout"),
		StderrPath:           filepath.Join(layout.outboxRun, "stderr"),
		TimeoutMS:            5_000,
		CapabilityLockSHA256: strings.Repeat("a", 64),
	}
}

func helperArgv(mode string, extra ...string) []string {
	argv := []string{os.Args[0], "-test.run=^TestWorkerHelperProcess$", "-bench-worker-helper=" + mode}
	return append(argv, extra...)
}

func writeRequest(t *testing.T, path string, request workerRequest) []byte {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func invoke(t *testing.T, layout testLayout) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{"run", "-request", layout.request, "-receipt", layout.receipt}, &stdout, &stderr, layout.cfg)
	if stdout.Len() != 0 {
		t.Fatalf("unexpected worker stdout: %q", stdout.String())
	}
	return code, stderr.String()
}

func readReceipt(t *testing.T, path string) workerReceipt {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt workerReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte("\n")) {
		t.Fatalf("receipt is not newline terminated: %q", raw)
	}
	return receipt
}

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestRunWritesSanitizedExecutionReceipt(t *testing.T) {
	layout := newTestLayout(t)
	request := layout.validRequest()
	raw := writeRequest(t, layout.request, request)
	t.Setenv("TOP_SECRET", "must-not-cross-worker-boundary")

	code, stderr := invoke(t, layout)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	wantOutput := strings.Join(childEnv, "\n") + "\n"
	stdout, err := os.ReadFile(request.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != wantOutput || strings.Contains(string(stdout), "TOP_SECRET") {
		t.Fatalf("child environment=%q, want exactly %q", stdout, wantOutput)
	}
	childStderr, err := os.ReadFile(request.StderrPath)
	if err != nil {
		t.Fatal(err)
	}
	if testing.CoverMode() == "" && len(childStderr) != 0 {
		t.Fatalf("child stderr=%q", childStderr)
	}
	if testing.CoverMode() != "" && !bytes.Contains(childStderr, []byte("GOCOVERDIR not set")) {
		t.Fatalf("covered helper stderr=%q", childStderr)
	}

	receipt := readReceipt(t, layout.receipt)
	if receipt.Schema != receiptSchema || !receipt.Provisional || receipt.RunID != request.RunID || receipt.RequestSHA256 != sha256Hex(raw) || receipt.CapabilityLockSHA256 != request.CapabilityLockSHA256 {
		t.Fatalf("receipt binding=%#v", receipt)
	}
	if strings.Join(receipt.Argv, "\x00") != strings.Join(request.Argv, "\x00") || receipt.CWD != request.CWD {
		t.Fatalf("receipt execution binding=%#v", receipt)
	}
	if receipt.Termination != "exited" || receipt.ExitCode != 0 || receipt.TermSignal != 0 || receipt.TimedOut {
		t.Fatalf("receipt termination=%#v", receipt)
	}
	if receipt.StdoutSHA256 != digestText(wantOutput) || receipt.StdoutBytes != int64(len(wantOutput)) || receipt.StderrSHA256 != digestText(string(childStderr)) || receipt.StderrBytes != int64(len(childStderr)) {
		t.Fatalf("receipt artifacts=%#v", receipt)
	}
	started, err := time.Parse(time.RFC3339Nano, receipt.StartedAt)
	if err != nil || started.Location() != time.UTC {
		t.Fatalf("started_at=%q err=%v", receipt.StartedAt, err)
	}
	ended, err := time.Parse(time.RFC3339Nano, receipt.EndedAt)
	if err != nil || ended.Location() != time.UTC || ended.Before(started) {
		t.Fatalf("ended_at=%q started_at=%q err=%v", receipt.EndedAt, receipt.StartedAt, err)
	}
	for _, path := range []string{request.StdoutPath, request.StderrPath, layout.receipt} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s mode=%v", path, info.Mode())
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || uint64(stat.Nlink) != 1 {
			t.Fatalf("%s link metadata=%#v", path, info.Sys())
		}
	}
}

func TestRunPassesStdinAndLiteralArgvWithoutInterpolation(t *testing.T) {
	t.Run("stdin", func(t *testing.T) {
		layout := newTestLayout(t)
		inputPath := filepath.Join(layout.inboxRun, "stdin")
		if err := os.WriteFile(inputPath, []byte("literal input\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		request := layout.validRequest()
		request.Argv = helperArgv("stdin")
		request.StdinPath = inputPath
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != 0 || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if got, err := os.ReadFile(request.StdoutPath); err != nil || string(got) != "literal input\n" {
			t.Fatalf("stdout=%q err=%v", got, err)
		}
	})

	t.Run("literal argv", func(t *testing.T) {
		layout := newTestLayout(t)
		marker := filepath.Join(layout.work, "interpolated")
		literal := "$(touch " + marker + ")"
		request := layout.validRequest()
		request.Argv = helperArgv("literal", literal, "; rm -rf nope", "*")
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != 0 || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("literal argv was interpreted: %v", err)
		}
		want := literal + "|; rm -rf nope|*\n"
		if got, err := os.ReadFile(request.StdoutPath); err != nil || string(got) != want {
			t.Fatalf("stdout=%q err=%v want=%q", got, err, want)
		}
	})
}

func TestRunPreservesChildStatusAndRecordsTimeout(t *testing.T) {
	t.Run("nonzero exit", func(t *testing.T) {
		layout := newTestLayout(t)
		request := layout.validRequest()
		request.Argv = helperArgv("exit23")
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != 23 || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		receipt := readReceipt(t, layout.receipt)
		if receipt.Termination != "exited" || receipt.ExitCode != 23 || receipt.TermSignal != 0 || receipt.TimedOut {
			t.Fatalf("receipt=%#v", receipt)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		layout := newTestLayout(t)
		request := layout.validRequest()
		request.Argv = helperArgv("sleep")
		request.TimeoutMS = 20
		writeRequest(t, layout.request, request)
		started := time.Now()
		code, stderr := invoke(t, layout)
		if code != exitTimeout || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("timeout took %v", elapsed)
		}
		receipt := readReceipt(t, layout.receipt)
		if receipt.Termination != "timed_out" || !receipt.TimedOut || receipt.ExitCode != -1 || receipt.TermSignal == 0 {
			t.Fatalf("receipt=%#v", receipt)
		}
	})

	t.Run("signal", func(t *testing.T) {
		layout := newTestLayout(t)
		request := layout.validRequest()
		request.Argv = helperArgv("signal")
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != 128+int(syscall.SIGTERM) || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		receipt := readReceipt(t, layout.receipt)
		if receipt.Termination != "signaled" || receipt.TimedOut || receipt.ExitCode != -1 || receipt.TermSignal != int(syscall.SIGTERM) {
			t.Fatalf("receipt=%#v", receipt)
		}
	})

	t.Run("start error", func(t *testing.T) {
		layout := newTestLayout(t)
		request := layout.validRequest()
		request.Argv = []string{"bench-worker-command-that-does-not-exist"}
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitNotFound || !strings.Contains(stderr, "start_error") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		receipt := readReceipt(t, layout.receipt)
		if receipt.Termination != "start_error" || receipt.ExitCode != -1 || receipt.TermSignal != 0 || receipt.TimedOut {
			t.Fatalf("receipt=%#v", receipt)
		}
		if receipt.StartedAt != receipt.EndedAt {
			t.Fatalf("pre-start failure timestamps started=%q ended=%q", receipt.StartedAt, receipt.EndedAt)
		}
	})
}

func TestRequestValidationFailsClosedBeforeExecution(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*workerRequest)
	}{
		{"schema", func(r *workerRequest) { r.Schema = "bench.worker-request/v2" }},
		{"run id", func(r *workerRequest) { r.RunID = "../escape" }},
		{"empty argv", func(r *workerRequest) { r.Argv = nil }},
		{"empty argv zero", func(r *workerRequest) { r.Argv = []string{""} }},
		{"zero timeout", func(r *workerRequest) { r.TimeoutMS = 0 }},
		{"large timeout", func(r *workerRequest) { r.TimeoutMS = maxTimeoutMS + 1 }},
		{"uppercase lock", func(r *workerRequest) { r.CapabilityLockSHA256 = strings.Repeat("A", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := newTestLayout(t)
			request := layout.validRequest()
			test.mutate(&request)
			writeRequest(t, layout.request, request)
			code, stderr := invoke(t, layout)
			if code != exitUsage || stderr == "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			for _, path := range []string{layout.receipt, request.StdoutPath, request.StderrPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid request created %s: %v", path, err)
				}
			}
		})
	}
}

func TestRunIDBindsEveryPerRunPath(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(testLayout, *workerRequest) string
	}{
		{"request namespace", func(layout testLayout, request *workerRequest) string {
			request.RunID = "different-run"
			return layout.receipt
		}},
		{"stdin namespace", func(layout testLayout, request *workerRequest) string {
			other := filepath.Join(layout.inbox, "different-run")
			if err := os.Mkdir(other, 0o700); err != nil {
				t.Fatal(err)
			}
			request.StdinPath = filepath.Join(other, "stdin")
			if err := os.WriteFile(request.StdinPath, []byte("other input"), 0o600); err != nil {
				t.Fatal(err)
			}
			return layout.receipt
		}},
		{"stdout namespace", func(layout testLayout, request *workerRequest) string {
			request.StdoutPath = filepath.Join(layout.outbox, "different-run", "stdout")
			return layout.receipt
		}},
		{"receipt namespace", func(layout testLayout, request *workerRequest) string {
			return filepath.Join(layout.outbox, "different-run", "receipt.json")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := newTestLayout(t)
			request := layout.validRequest()
			receipt := test.mutate(layout, &request)
			writeRequest(t, layout.request, request)
			var stdout, stderr bytes.Buffer
			code := runWithConfig([]string{"run", "-request", layout.request, "-receipt", receipt}, &stdout, &stderr, layout.cfg)
			if code != exitUsage || !strings.Contains(stderr.String(), "run_id namespace") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, path := range []string{layout.receipt, request.StdoutPath, request.StderrPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("namespace mismatch created %s: %v", path, err)
				}
			}
		})
	}
}

func TestRequestJSONIsStrictAndUnambiguous(t *testing.T) {
	layout := newTestLayout(t)
	request := layout.validRequest()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), bytes.TrimSuffix(raw, []byte("}"))...), []byte(`,"extra":true}`)...)
	if _, err := decodeRequest(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field err=%v", err)
	}
	duplicate := []byte(strings.Replace(string(raw), `"schema":`, `"schema":"bench.worker-request/v1","schema":`, 1))
	if _, err := decodeRequest(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate field err=%v", err)
	}
	caseAlias := append(append([]byte(nil), bytes.TrimSuffix(raw, []byte("}"))...), []byte(`,"Schema":"bench.worker-request/v1"}`)...)
	if _, err := decodeRequest(caseAlias); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("case-aliased field err=%v", err)
	}
	if _, err := decodeRequest(append(raw, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}

func TestPhysicalPathPolicyRejectsEscapesAndSymlinks(t *testing.T) {
	t.Run("request symlink", func(t *testing.T) {
		layout := newTestLayout(t)
		target := filepath.Join(layout.inboxRun, "target.json")
		writeRequest(t, target, layout.validRequest())
		if err := os.Symlink(target, layout.request); err != nil {
			t.Fatal(err)
		}
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "symlink") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("cwd symlink", func(t *testing.T) {
		layout := newTestLayout(t)
		real := filepath.Join(layout.work, "real")
		link := filepath.Join(layout.work, "link")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		request := layout.validRequest()
		request.CWD = link
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "symlink") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("stdin symlink", func(t *testing.T) {
		layout := newTestLayout(t)
		real := filepath.Join(layout.inboxRun, "real-stdin")
		link := filepath.Join(layout.inboxRun, "stdin")
		if err := os.WriteFile(real, []byte("input"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		request := layout.validRequest()
		request.Argv = helperArgv("stdin")
		request.StdinPath = link
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "symlink") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("outbox escape", func(t *testing.T) {
		layout := newTestLayout(t)
		request := layout.validRequest()
		request.StdoutPath = filepath.Join(layout.root, "escaped-stdout")
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "beneath") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if _, err := os.Stat(request.StdoutPath); !os.IsNotExist(err) {
			t.Fatalf("escape created: %v", err)
		}
	})

	t.Run("outbox parent symlink", func(t *testing.T) {
		layout := newTestLayout(t)
		real := filepath.Join(layout.outbox, "real")
		if err := os.Remove(layout.outboxRun); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, layout.outboxRun); err != nil {
			t.Fatal(err)
		}
		request := layout.validRequest()
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "symlink") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("control path", func(t *testing.T) {
		layout := newTestLayout(t)
		request := layout.validRequest()
		request.StdoutPath = filepath.Join(layout.outboxRun, "bad\npath")
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "control") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})
}

func TestOutputsAndReceiptAreExclusive(t *testing.T) {
	for _, existing := range []string{"stdout", "receipt"} {
		t.Run(existing, func(t *testing.T) {
			layout := newTestLayout(t)
			request := layout.validRequest()
			writeRequest(t, layout.request, request)
			path := request.StdoutPath
			if existing == "receipt" {
				path = layout.receipt
			}
			if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, stderr := invoke(t, layout)
			if code != exitUsage || !strings.Contains(stderr, "already exists") {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
				t.Fatalf("existing file=%q err=%v", got, err)
			}
		})
	}
}

func TestMultiplyLinkedInboxFilesAreRejected(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		layout := newTestLayout(t)
		target := filepath.Join(layout.root, "controller-request.json")
		writeRequest(t, target, layout.validRequest())
		if err := os.Link(target, layout.request); err != nil {
			t.Fatal(err)
		}
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "hard links") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if _, err := os.Stat(layout.receipt); !os.IsNotExist(err) {
			t.Fatalf("receipt created for multiply-linked request: %v", err)
		}
	})

	t.Run("stdin", func(t *testing.T) {
		layout := newTestLayout(t)
		target := filepath.Join(layout.root, "controller-stdin")
		stdin := filepath.Join(layout.inboxRun, "stdin")
		if err := os.WriteFile(target, []byte("input"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(target, stdin); err != nil {
			t.Fatal(err)
		}
		request := layout.validRequest()
		request.Argv = helperArgv("stdin")
		request.StdinPath = stdin
		writeRequest(t, layout.request, request)
		code, stderr := invoke(t, layout)
		if code != exitUsage || !strings.Contains(stderr, "hard links") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if _, err := os.Stat(layout.receipt); !os.IsNotExist(err) {
			t.Fatalf("receipt created for multiply-linked stdin: %v", err)
		}
	})
}

func TestOutputHardLinkInvalidatesReceipt(t *testing.T) {
	layout := newTestLayout(t)
	request := layout.validRequest()
	link := filepath.Join(layout.work, "stdout-link")
	request.Argv = helperArgv("hardlink",
		"-bench-worker-helper-source="+request.StdoutPath,
		"-bench-worker-helper-dest="+link,
	)
	writeRequest(t, layout.request, request)
	code, stderr := invoke(t, layout)
	if code != exitWorkerError || !strings.Contains(stderr, "hard links") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("helper did not create hard link: %v", err)
	}
	if _, err := os.Stat(layout.receipt); !os.IsNotExist(err) {
		t.Fatalf("untrusted receipt remains: %v", err)
	}
}

func TestCLIRequiresExactPrivateInterface(t *testing.T) {
	layout := newTestLayout(t)
	for _, args := range [][]string{
		nil,
		{"other"},
		{"run"},
		{"run", "-request", layout.request},
		{"run", "-request", layout.request, "-receipt", layout.receipt, "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runWithConfig(args, &stdout, &stderr, layout.cfg); code != exitUsage {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runWithConfig([]string{"help"}, &stdout, &stderr, layout.cfg); code != 0 || !strings.Contains(stdout.String(), "bench-worker run") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIStatusNeverWrapsSignalToSuccess(t *testing.T) {
	for _, signal := range []int{1, 9, 127, 128, 255} {
		execution := childExecution{termination: "signaled", termSignal: signal}
		if status := execution.cliStatus(); status <= 0 || status > 255 {
			t.Fatalf("signal=%s status=%d", strconv.Itoa(signal), status)
		}
	}
}
