// Command bench-worker is the unprivileged process-execution seam used inside
// a Bench worker guest. It executes one already-authorized literal argv and
// records process facts; it does not interpret a task or decide completion.
package main

import (
	"bytes"
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
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	requestSchema = "bench.worker-request/v1"
	receiptSchema = "bench.worker-receipt/v1"

	exitUsage       = 2
	exitTimeout     = 124
	exitWorkerError = 125
	exitCannotExec  = 126
	exitNotFound    = 127

	maxRequestBytes = 1 << 20
	maxPathBytes    = 4096
	maxRunIDBytes   = 128
	maxArgCount     = 256
	maxArgBytes     = 128 << 10
	maxTimeoutMS    = 60 * 60 * 1000

	fixedPath = "/opt/bench-suite/bin:/usr/bin:/bin"
)

var (
	portableRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	lowerSHA256   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	childEnv      = []string{
		"PATH=" + fixedPath,
		"HOME=/var/lib/bench-worker",
		"TMPDIR=/tmp",
		"LANG=C.UTF-8",
	}
)

type workerConfig struct {
	workRoot   string
	inboxRoot  string
	outboxRoot string
}

func productionConfig() workerConfig {
	return workerConfig{
		workRoot:   "/work",
		inboxRoot:  "/run/bench/inbox",
		outboxRoot: "/run/bench/outbox",
	}
}

type workerRequest struct {
	Schema               string   `json:"schema"`
	RunID                string   `json:"run_id"`
	Argv                 []string `json:"argv"`
	CWD                  string   `json:"cwd"`
	StdinPath            string   `json:"stdin_path,omitempty"`
	StdoutPath           string   `json:"stdout_path"`
	StderrPath           string   `json:"stderr_path"`
	TimeoutMS            int64    `json:"timeout_ms"`
	CapabilityLockSHA256 string   `json:"capability_lock_sha256"`
}

type workerReceipt struct {
	Schema               string   `json:"schema"`
	Provisional          bool     `json:"provisional"`
	RunID                string   `json:"run_id"`
	RequestSHA256        string   `json:"request_sha256"`
	CapabilityLockSHA256 string   `json:"capability_lock_sha256"`
	Argv                 []string `json:"argv"`
	CWD                  string   `json:"cwd"`
	StartedAt            string   `json:"started_at"`
	EndedAt              string   `json:"ended_at"`
	Termination          string   `json:"termination"`
	ExitCode             int      `json:"exit_code"`
	TermSignal           int      `json:"term_signal"`
	TimedOut             bool     `json:"timed_out"`
	StdoutSHA256         string   `json:"stdout_sha256"`
	StdoutBytes          int64    `json:"stdout_bytes"`
	StderrSHA256         string   `json:"stderr_sha256"`
	StderrBytes          int64    `json:"stderr_bytes"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithConfig(args, stdout, stderr, productionConfig())
}

// runWithConfig is the sole root override. It is unexported so production
// callers can only use the fixed guest paths; tests use it with temporary
// directory trees.
func runWithConfig(args []string, stdout, stderr io.Writer, cfg workerConfig) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		printUsage(stdout)
		return 0
	}
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(stderr, "bench-worker: expected run")
		printUsage(stderr)
		return exitUsage
	}

	flags := flag.NewFlagSet("bench-worker run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requestPath := flags.String("request", "", "request JSON in the guest inbox")
	receiptPath := flags.String("receipt", "", "exclusive receipt JSON in the guest outbox")
	flags.Usage = func() { printUsage(stderr) }
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exitUsage
	}
	if flags.NArg() != 0 || *requestPath == "" || *receiptPath == "" {
		fmt.Fprintln(stderr, "bench-worker: run requires exactly -request FILE -receipt FILE")
		return exitUsage
	}

	status, err := runOne(*requestPath, *receiptPath, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "bench-worker:", err)
	}
	return status
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: bench-worker run -request FILE -receipt FILE")
}

type confinedRoot struct {
	name       string
	configured string
	physical   string
	dir        *os.Root
}

func openConfinedRoot(name, path string) (*confinedRoot, error) {
	if err := validateAbsolutePath(path, name+" root"); err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("open %s root: %w", name, err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s root must not be a symlink", name)
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("open %s root: %w", name, err)
	}
	info, err := os.Stat(physical)
	if err != nil {
		return nil, fmt.Errorf("stat %s root: %w", name, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s root is not a directory", name)
	}
	dir, err := os.OpenRoot(physical)
	if err != nil {
		return nil, fmt.Errorf("open %s root: %w", name, err)
	}
	return &confinedRoot{name: name, configured: path, physical: physical, dir: dir}, nil
}

type confinedPath struct {
	root      *confinedRoot
	requested string
	rel       string
	physical  string
}

func (r *confinedRoot) resolve(path, label string) (confinedPath, error) {
	if err := validateAbsolutePath(path, label); err != nil {
		return confinedPath{}, err
	}
	rel, err := filepath.Rel(r.configured, path)
	if err != nil || relEscapes(rel) {
		return confinedPath{}, fmt.Errorf("%s must be beneath %s", label, r.configured)
	}
	return confinedPath{
		root:      r,
		requested: path,
		rel:       rel,
		physical:  filepath.Join(r.physical, rel),
	}, nil
}

func relEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

func validateAbsolutePath(path, label string) error {
	if path == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if !utf8.ValidString(path) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	if len(path) > maxPathBytes {
		return fmt.Errorf("%s is too long", label)
	}
	if hasControl(path) {
		return fmt.Errorf("%s contains a control character", label)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be absolute", label)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("%s must be a clean path", label)
	}
	return nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func (p confinedPath) inspect(finalMustExist bool) (fs.FileInfo, error) {
	if p.rel == "." {
		info, err := p.root.dir.Lstat(".")
		if err != nil {
			return nil, err
		}
		return info, nil
	}
	parts := strings.Split(p.rel, string(filepath.Separator))
	current := ""
	for i, part := range parts {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := p.root.dir.Lstat(current)
		last := i == len(parts)-1
		if err != nil {
			if last && !finalMustExist && errors.Is(err, fs.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s contains a symlink at %s", p.requested, current)
		}
		if !last && !info.IsDir() {
			return nil, fmt.Errorf("%s has a non-directory parent", p.requested)
		}
	}
	return p.root.dir.Lstat(p.rel)
}

func (p confinedPath) requireDirectory(label string) error {
	info, err := p.inspect(true)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be a directory", label)
	}
	return nil
}

func (p confinedPath) requireRegular(label string) error {
	info, err := p.inspect(true)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", label)
	}
	return nil
}

func (p confinedPath) requireMissing(label string) error {
	info, err := p.inspect(false)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if info != nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must not be a symlink", label)
		}
		return fmt.Errorf("%s already exists", label)
	}
	return nil
}

func (p confinedPath) openRegular(label string) (*os.File, error) {
	f, err := p.root.dir.OpenFile(p.rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	if err := validateOpenRegular(p, f, true); err != nil {
		f.Close()
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	return f, nil
}

func (p confinedPath) openDirectory(label string) (*os.File, error) {
	dir, err := p.root.dir.Open(p.rel)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	opened, err := dir.Stat()
	if err != nil {
		dir.Close()
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	current, err := p.inspect(true)
	if err != nil || !opened.IsDir() || !current.IsDir() || !os.SameFile(opened, current) {
		dir.Close()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", label, err)
		}
		return nil, fmt.Errorf("open %s: path no longer names the opened directory", label)
	}
	return dir, nil
}

type createdFile struct {
	path confinedPath
	file *os.File
}

func createExclusive(path confinedPath, label string) (*createdFile, error) {
	f, err := path.root.dir.OpenFile(path.rel, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", label, err)
	}
	created := &createdFile{path: path, file: f}
	if err := validateOpenRegular(path, f, true); err != nil {
		discardCreated(created)
		return nil, fmt.Errorf("create %s: %w", label, err)
	}
	return created, nil
}

func validateOpenRegular(path confinedPath, f *os.File, singleLink bool) error {
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if singleLink {
		if err := requireSingleLink(opened); err != nil {
			return err
		}
	}
	current, err := path.inspect(true)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errors.New("path no longer names the opened regular file")
	}
	return nil
}

func requireSingleLink(info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot verify regular-file link count")
	}
	if uint64(stat.Nlink) != 1 {
		return fmt.Errorf("regular file has %d hard links", uint64(stat.Nlink))
	}
	return nil
}

func discardCreated(created *createdFile) {
	if created == nil || created.file == nil {
		return
	}
	opened, statErr := created.file.Stat()
	_ = created.file.Close()
	created.file = nil
	if statErr != nil {
		return
	}
	current, err := created.path.inspect(true)
	if err == nil && os.SameFile(opened, current) {
		_ = created.path.root.dir.Remove(created.path.rel)
	}
}

func runOne(requestName, receiptName string, cfg workerConfig) (int, error) {
	work, err := openConfinedRoot("work", cfg.workRoot)
	if err != nil {
		return exitWorkerError, err
	}
	defer work.dir.Close()
	inbox, err := openConfinedRoot("inbox", cfg.inboxRoot)
	if err != nil {
		return exitWorkerError, err
	}
	defer inbox.dir.Close()
	outbox, err := openConfinedRoot("outbox", cfg.outboxRoot)
	if err != nil {
		return exitWorkerError, err
	}
	defer outbox.dir.Close()

	requestPath, err := inbox.resolve(requestName, "request path")
	if err != nil {
		return exitUsage, err
	}
	if err := requestPath.requireRegular("request path"); err != nil {
		return exitUsage, err
	}
	requestFile, err := requestPath.openRegular("request")
	if err != nil {
		return exitUsage, err
	}
	raw, err := readBounded(requestFile, maxRequestBytes)
	closeErr := requestFile.Close()
	if err != nil {
		return exitUsage, fmt.Errorf("read request: %w", err)
	}
	if closeErr != nil {
		return exitWorkerError, fmt.Errorf("close request: %w", closeErr)
	}
	requestDigest := sha256Hex(raw)

	request, err := decodeRequest(raw)
	if err != nil {
		return exitUsage, fmt.Errorf("request: %w", err)
	}
	if err := validateRequest(request); err != nil {
		return exitUsage, fmt.Errorf("request: %w", err)
	}

	cwd, err := work.resolve(request.CWD, "cwd")
	if err != nil {
		return exitUsage, err
	}
	if err := cwd.requireDirectory("cwd"); err != nil {
		return exitUsage, err
	}
	cwdDir, err := cwd.openDirectory("cwd")
	if err != nil {
		return exitUsage, err
	}
	defer cwdDir.Close()

	var stdinPath confinedPath
	if request.StdinPath != "" {
		stdinPath, err = inbox.resolve(request.StdinPath, "stdin_path")
		if err != nil {
			return exitUsage, err
		}
		if err := stdinPath.requireRegular("stdin_path"); err != nil {
			return exitUsage, err
		}
	}
	stdoutPath, err := outbox.resolve(request.StdoutPath, "stdout_path")
	if err != nil {
		return exitUsage, err
	}
	stderrPath, err := outbox.resolve(request.StderrPath, "stderr_path")
	if err != nil {
		return exitUsage, err
	}
	receiptPath, err := outbox.resolve(receiptName, "receipt path")
	if err != nil {
		return exitUsage, err
	}
	if err := validateRunNamespace(request, requestPath, stdinPath, stdoutPath, stderrPath, receiptPath); err != nil {
		return exitUsage, err
	}
	for label, path := range map[string]confinedPath{
		"stdout_path":  stdoutPath,
		"stderr_path":  stderrPath,
		"receipt path": receiptPath,
	} {
		if err := path.requireMissing(label); err != nil {
			return exitUsage, err
		}
	}
	if stdoutPath.physical == stderrPath.physical || stdoutPath.physical == receiptPath.physical || stderrPath.physical == receiptPath.physical {
		return exitUsage, errors.New("stdout_path, stderr_path, and receipt path must be distinct")
	}

	var stdin *os.File
	if request.StdinPath != "" {
		stdin, err = stdinPath.openRegular("stdin_path")
		if err != nil {
			return exitUsage, err
		}
		defer stdin.Close()
	}

	stdoutFile, err := createExclusive(stdoutPath, "stdout_path")
	if err != nil {
		return exitWorkerError, err
	}
	stderrFile, err := createExclusive(stderrPath, "stderr_path")
	if err != nil {
		discardCreated(stdoutFile)
		return exitWorkerError, err
	}
	receiptFile, err := createExclusive(receiptPath, "receipt path")
	if err != nil {
		discardCreated(stderrFile)
		discardCreated(stdoutFile)
		return exitWorkerError, err
	}
	if err := syncParent(receiptPath); err != nil {
		discardCreated(receiptFile)
		discardCreated(stderrFile)
		discardCreated(stdoutFile)
		return exitWorkerError, fmt.Errorf("sync output directory: %w", err)
	}
	// The exact receipt path is reserved as a zero-byte O_EXCL regular file.
	// Zero bytes, JSON without the final newline commit marker, and malformed
	// JSON are non-terminal. writeReceipt appends that marker only after the
	// complete body is durable. The receipt remains explicitly provisional:
	// an external controller must wait for an empty/stopped service cgroup,
	// re-hash the artifacts, and seal its own authoritative receipt.

	// Recheck the directory immediately before Start. The path was already
	// checked before artifact reservation; this closes that avoidable race.
	if err := cwd.requireDirectory("cwd"); err != nil {
		discardCreated(receiptFile)
		discardCreated(stderrFile)
		discardCreated(stdoutFile)
		return exitUsage, err
	}
	execution := runChild(request, cwd.physical, cwdDir, stdin, stdoutFile.file, stderrFile.file)
	if execution.issue != nil && execution.termination != "start_error" {
		discardCreated(receiptFile)
		_ = stdoutFile.file.Close()
		_ = stderrFile.file.Close()
		return exitWorkerError, fmt.Errorf("child %s: %w", execution.termination, execution.issue)
	}
	stdoutDigest, stdoutBytes, err := digestCreated(stdoutFile)
	if err != nil {
		discardCreated(receiptFile)
		_ = stdoutFile.file.Close()
		_ = stderrFile.file.Close()
		return exitWorkerError, fmt.Errorf("validate stdout artifact: %w", err)
	}
	stderrDigest, stderrBytes, err := digestCreated(stderrFile)
	if err != nil {
		discardCreated(receiptFile)
		_ = stdoutFile.file.Close()
		_ = stderrFile.file.Close()
		return exitWorkerError, fmt.Errorf("validate stderr artifact: %w", err)
	}
	if err := stdoutFile.file.Close(); err != nil {
		discardCreated(receiptFile)
		_ = stderrFile.file.Close()
		return exitWorkerError, fmt.Errorf("close stdout artifact: %w", err)
	}
	stdoutFile.file = nil
	if err := stderrFile.file.Close(); err != nil {
		discardCreated(receiptFile)
		return exitWorkerError, fmt.Errorf("close stderr artifact: %w", err)
	}
	stderrFile.file = nil

	receipt := workerReceipt{
		Schema:               receiptSchema,
		Provisional:          true,
		RunID:                request.RunID,
		RequestSHA256:        requestDigest,
		CapabilityLockSHA256: request.CapabilityLockSHA256,
		Argv:                 append([]string(nil), request.Argv...),
		CWD:                  request.CWD,
		StartedAt:            execution.started.UTC().Format(time.RFC3339Nano),
		EndedAt:              execution.ended.UTC().Format(time.RFC3339Nano),
		Termination:          execution.termination,
		ExitCode:             execution.exitCode,
		TermSignal:           execution.termSignal,
		TimedOut:             execution.timedOut,
		StdoutSHA256:         stdoutDigest,
		StdoutBytes:          stdoutBytes,
		StderrSHA256:         stderrDigest,
		StderrBytes:          stderrBytes,
	}
	if err := writeReceipt(receiptFile, receipt); err != nil {
		discardCreated(receiptFile)
		return exitWorkerError, fmt.Errorf("write receipt: %w", err)
	}

	status := execution.cliStatus()
	if execution.issue != nil {
		return status, fmt.Errorf("child %s: %w", execution.termination, execution.issue)
	}
	return status, nil
}

func syncParent(path confinedPath) error {
	parent := filepath.Dir(path.rel)
	dir, err := path.root.dir.Open(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("artifact parent is not a directory")
	}
	return dir.Sync()
}

func validateRunNamespace(request workerRequest, requestPath, stdinPath, stdoutPath, stderrPath, receiptPath confinedPath) error {
	runDir := request.RunID
	want := map[string]struct {
		got  string
		want string
	}{
		"request path": {requestPath.rel, filepath.Join(runDir, "request.json")},
		"stdout_path":  {stdoutPath.rel, filepath.Join(runDir, "stdout")},
		"stderr_path":  {stderrPath.rel, filepath.Join(runDir, "stderr")},
		"receipt path": {receiptPath.rel, filepath.Join(runDir, "receipt.json")},
	}
	for label, paths := range want {
		if paths.got != paths.want {
			return fmt.Errorf("%s must match run_id namespace %q", label, request.RunID)
		}
	}
	if request.StdinPath != "" {
		prefix := runDir + string(filepath.Separator)
		if stdinPath.rel == runDir || !strings.HasPrefix(stdinPath.rel, prefix) {
			return fmt.Errorf("stdin_path must remain in run_id namespace %q", request.RunID)
		}
	}
	return nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return raw, nil
}

func decodeRequest(raw []byte) (workerRequest, error) {
	if !utf8.Valid(raw) {
		return workerRequest{}, errors.New("request is not valid UTF-8 JSON")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return workerRequest{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return workerRequest{}, err
	}
	allowed := map[string]bool{
		"schema": true, "run_id": true, "argv": true, "cwd": true,
		"stdin_path": true, "stdout_path": true, "stderr_path": true,
		"timeout_ms": true, "capability_lock_sha256": true,
	}
	for name := range fields {
		if !allowed[name] {
			return workerRequest{}, fmt.Errorf("unknown field %q", name)
		}
	}
	var request workerRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return workerRequest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return workerRequest{}, errors.New("trailing JSON value")
		}
		return workerRequest{}, err
	}
	return request, nil
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err == nil {
		return fmt.Errorf("trailing JSON token %v", token)
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON field %q", name)
			}
			seen[name] = true
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return errors.New("malformed JSON delimiter")
	}
	return nil
}

func validateRequest(request workerRequest) error {
	if request.Schema != requestSchema {
		return fmt.Errorf("schema must be %q", requestSchema)
	}
	if len(request.RunID) == 0 || len(request.RunID) > maxRunIDBytes || !portableRunID.MatchString(request.RunID) || request.RunID == "." || request.RunID == ".." {
		return errors.New("run_id is not a portable identifier")
	}
	if len(request.Argv) == 0 {
		return errors.New("argv must be a nonempty array")
	}
	if len(request.Argv) > maxArgCount {
		return fmt.Errorf("argv has more than %d entries", maxArgCount)
	}
	totalBytes := 0
	for i, arg := range request.Argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return fmt.Errorf("argv[%d] contains NUL", i)
		}
		totalBytes += len(arg) + 1
	}
	if request.Argv[0] == "" {
		return errors.New("argv[0] is empty")
	}
	if hasControl(request.Argv[0]) {
		return errors.New("argv[0] contains a control character")
	}
	if totalBytes > maxArgBytes {
		return fmt.Errorf("argv exceeds %d bytes", maxArgBytes)
	}
	if request.TimeoutMS < 1 || request.TimeoutMS > maxTimeoutMS {
		return fmt.Errorf("timeout_ms must be between 1 and %d", maxTimeoutMS)
	}
	if !lowerSHA256.MatchString(request.CapabilityLockSHA256) {
		return errors.New("capability_lock_sha256 must be exactly 64 lowercase hexadecimal characters")
	}
	return nil
}

type childExecution struct {
	started     time.Time
	ended       time.Time
	termination string
	exitCode    int
	termSignal  int
	timedOut    bool
	issue       error
}

func runChild(request workerRequest, cwd string, cwdDir, stdin, stdout, stderr *os.File) childExecution {
	result := childExecution{exitCode: -1}
	executable, err := resolveExecutable(request.Argv[0])
	if err != nil {
		// No process was started. Equal timestamps record the failed start
		// attempt without suggesting that a child ran.
		result.started = time.Now().UTC()
		result.ended = result.started
		result.termination = "start_error"
		result.issue = err
		return result
	}
	command := &exec.Cmd{
		Path:        executable,
		Args:        append([]string(nil), request.Argv...),
		Dir:         pinnedDirectory(cwdDir, cwd),
		Env:         append([]string(nil), childEnv...),
		Stdin:       stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	// started_at is the attempt timestamp immediately before Start. The
	// termination field, rather than this timestamp, says whether a child ran.
	result.started = time.Now().UTC()
	if err := command.Start(); err != nil {
		result.ended = time.Now().UTC()
		result.termination = "start_error"
		result.issue = err
		return result
	}

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	timer := time.NewTimer(time.Duration(request.TimeoutMS) * time.Millisecond)
	var waitErr error
	select {
	case waitErr = <-done:
		timer.Stop()
	case <-timer.C:
		select {
		case waitErr = <-done:
		default:
			result.timedOut = true
			if err := killProcessGroup(command.Process); err != nil {
				result.issue = err
			}
			waitErr = <-done
		}
	}
	// A command that exits while leaving descendants behind must not leave
	// writers attached to the artifact descriptors.
	if err := killProcessGroup(command.Process); err != nil && result.issue == nil {
		result.issue = err
	}
	result.ended = time.Now().UTC()

	if command.ProcessState == nil {
		result.termination = "worker_error"
		if result.issue == nil {
			result.issue = errors.New("child has no process state")
		}
		return result
	}
	if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.termSignal = int(status.Signal())
	}
	if result.timedOut {
		result.termination = "timed_out"
		return result
	}
	if result.termSignal != 0 {
		result.termination = "signaled"
		return result
	}
	result.exitCode = command.ProcessState.ExitCode()
	result.termination = "exited"
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			result.termination = "worker_error"
			result.issue = waitErr
		}
	}
	return result
}

func pinnedDirectory(dir *os.File, fallback string) string {
	switch runtime.GOOS {
	case "linux":
		return fmt.Sprintf("/proc/self/fd/%d", dir.Fd())
	default:
		return fallback
	}
}

func resolveExecutable(argv0 string) (string, error) {
	if strings.ContainsRune(argv0, filepath.Separator) {
		return argv0, nil
	}
	var permissionErr error
	for _, dir := range strings.Split(fixedPath, ":") {
		candidate := filepath.Join(dir, argv0)
		info, err := os.Stat(candidate)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && permissionErr == nil {
				permissionErr = err
			}
			continue
		}
		if info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
		if permissionErr == nil {
			permissionErr = fs.ErrPermission
		}
	}
	if permissionErr != nil {
		return "", fmt.Errorf("resolve executable %q: %w", argv0, permissionErr)
	}
	return "", fmt.Errorf("resolve executable %q: %w", argv0, exec.ErrNotFound)
}

func killProcessGroup(process *os.Process) error {
	if process == nil {
		return nil
	}
	err := syscall.Kill(-process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if fallback := process.Kill(); fallback == nil || errors.Is(fallback, os.ErrProcessDone) {
		return nil
	} else {
		return fmt.Errorf("kill child process group: %v; kill child: %w", err, fallback)
	}
}

func (execution childExecution) cliStatus() int {
	if execution.timedOut {
		return exitTimeout
	}
	if execution.issue != nil && execution.termination != "start_error" {
		return exitWorkerError
	}
	switch execution.termination {
	case "exited":
		if execution.exitCode >= 0 && execution.exitCode <= 255 {
			return execution.exitCode
		}
	case "signaled":
		status := 128 + execution.termSignal
		if status > 255 {
			return 255
		}
		if status > 0 {
			return status
		}
	case "start_error":
		if errors.Is(execution.issue, exec.ErrNotFound) || errors.Is(execution.issue, fs.ErrNotExist) {
			return exitNotFound
		}
		if errors.Is(execution.issue, fs.ErrPermission) {
			return exitCannotExec
		}
	}
	return exitWorkerError
}

func digestCreated(created *createdFile) (string, int64, error) {
	if err := created.file.Sync(); err != nil {
		return "", 0, err
	}
	if err := validateOpenRegular(created.path, created.file, true); err != nil {
		return "", 0, err
	}
	if _, err := created.file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, created.file)
	if err != nil {
		return "", 0, err
	}
	info, err := created.file.Stat()
	if err != nil {
		return "", 0, err
	}
	if info.Size() != count {
		return "", 0, errors.New("artifact changed while hashing")
	}
	if err := validateOpenRegular(created.path, created.file, true); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func writeReceipt(created *createdFile, receipt workerReceipt) error {
	if err := validateOpenRegular(created.path, created.file, true); err != nil {
		return err
	}
	info, err := created.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != 0 {
		return errors.New("reserved receipt was modified before commit")
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(receipt); err != nil {
		return err
	}
	payload := body.Bytes()
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		return errors.New("receipt encoder omitted commit marker")
	}
	if _, err := created.file.Write(payload[:len(payload)-1]); err != nil {
		return err
	}
	if err := created.file.Sync(); err != nil {
		return err
	}
	if err := validateOpenRegular(created.path, created.file, true); err != nil {
		return err
	}
	info, err = created.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != int64(len(payload)-1) {
		return errors.New("receipt body changed before commit")
	}
	if _, err := created.file.Write([]byte{'\n'}); err != nil {
		return err
	}
	if err := created.file.Sync(); err != nil {
		return err
	}
	if err := validateOpenRegular(created.path, created.file, true); err != nil {
		return err
	}
	info, err = created.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != int64(len(payload)) {
		return errors.New("receipt changed while committing")
	}
	if err := created.file.Close(); err != nil {
		return err
	}
	created.file = nil
	return nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
