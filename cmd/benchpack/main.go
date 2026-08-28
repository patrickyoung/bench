// Command benchpack builds one relocatable Bench suite from sibling source
// repositories pinned by internal/suite/manifest.json.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
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
	"runtime"
	runtimedebug "runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/patrickyoung/bench/internal/suite"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("benchpack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspace := flags.String("workspace", ".benchpack/sources", "directory containing or caching component repositories")
	self := flags.String("self", ".", "Bench source checkout (revision self)")
	out := flags.String("out", "dist", "archive output directory")
	targetOS := flags.String("os", runtime.GOOS, "target GOOS")
	targetArch := flags.String("arch", runtime.GOARCH, "target GOARCH")
	fetch := flags.Bool("fetch", false, "clone missing components at their pinned revisions")
	allowDirty := flags.Bool("allow-dirty", false, "build a development archive from modified sources")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "benchpack: unexpected arguments")
		return 2
	}
	m, err := suite.Current()
	if err != nil {
		return report(stderr, err)
	}
	archive, err := build(m, options{
		Workspace:  *workspace,
		Self:       *self,
		Out:        *out,
		GOOS:       strings.TrimSpace(*targetOS),
		GOARCH:     strings.TrimSpace(*targetArch),
		Fetch:      *fetch,
		AllowDirty: *allowDirty,
	})
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintln(stdout, archive)
	return 0
}

type options struct {
	Workspace  string
	Self       string
	Out        string
	GOOS       string
	GOARCH     string
	Fetch      bool
	AllowDirty bool
}

func build(m suite.Manifest, o options) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if o.GOOS == "" || o.GOARCH == "" {
		return "", errors.New("target OS and architecture are required")
	}
	if o.GOOS != "darwin" && o.GOOS != "linux" {
		return "", fmt.Errorf("unsupported target OS %q; the suite currently requires macOS or Linux", o.GOOS)
	}
	if !targetPart(o.GOARCH) {
		return "", fmt.Errorf("invalid target architecture %q", o.GOARCH)
	}
	workspace, err := filepath.Abs(o.Workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	sources, err := sourcePaths(m, workspace, o.Self, o.Fetch)
	if err != nil {
		return "", err
	}
	if o.Fetch {
		if err := fetchMissing(m, workspace, sources); err != nil {
			return "", err
		}
	}
	resolved, dirty, err := resolve(m, sources, o.AllowDirty)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("bench-suite-%s-%s-%s", m.Version, o.GOOS, o.GOARCH)
	if dirty {
		name += "-dirty"
	}
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return "", fmt.Errorf("resolve output: %w", err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	stage, err := os.MkdirTemp(out, ".benchpack-")
	if err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	root := filepath.Join(stage, name)
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		return "", err
	}
	for i, component := range resolved.Components {
		source := sources[component.Name]
		switch component.Kind {
		case "go":
			if err := buildGo(source, filepath.Join(root, "bin", executable(component.Name, o.GOOS)), o); err != nil {
				return "", fmt.Errorf("build %s: %w", component.Name, err)
			}
		case "files":
			if err := copyFile(filepath.Join(source, component.Entry), filepath.Join(root, "bin", component.Name), 0o755); err != nil {
				return "", fmt.Errorf("copy %s: %w", component.Name, err)
			}
			for _, asset := range component.Assets {
				if err := copyTree(filepath.Join(source, asset), filepath.Join(root, asset)); err != nil {
					return "", fmt.Errorf("copy %s asset %s: %w", component.Name, asset, err)
				}
			}
		}
		license := filepath.Base(component.LicenseFile)
		if err := copyFile(filepath.Join(source, component.LicenseFile), filepath.Join(root, "licenses", component.Name, license), 0o644); err != nil {
			return "", fmt.Errorf("copy %s license: %w", component.Name, err)
		}
		resolved.Components[i] = component
	}
	toolDir, err := runnableTools(root, stage, sources, resolved.Components, o)
	if err != nil {
		return "", err
	}
	if err := verifyVersions(root, toolDir, resolved); err != nil {
		return "", err
	}
	if err := syncDraft(root, stage, toolDir); err != nil {
		return "", err
	}
	inventory, err := writeGoModules(root, resolved, o)
	if err != nil {
		return "", err
	}
	if err := collectThirdPartyLicenses(root, inventory); err != nil {
		return "", err
	}
	manifest, err := suite.JSON(resolved)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "suite.json"), manifest, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "INSTALL.md"), []byte(installText(name)), 0o644); err != nil {
		return "", err
	}
	installVersion := resolved.Version
	if dirty {
		installVersion += "-dirty"
	}
	if err := os.WriteFile(filepath.Join(root, "install.sh"), []byte(installScript(installVersion, componentNames(resolved.Components))), 0o755); err != nil {
		return "", err
	}
	if err := smokeBundle(root, o); err != nil {
		return "", err
	}
	if err := writeChecksums(root); err != nil {
		return "", err
	}
	if err := smokeInstall(root, resolved, o); err != nil {
		return "", err
	}
	archive := filepath.Join(out, name+".tar.gz")
	if err := writeArchive(archive, stage, name); err != nil {
		return "", err
	}
	if err := writeArchiveChecksum(archive); err != nil {
		return "", err
	}
	return archive, nil
}

func targetPart(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func sourcePaths(m suite.Manifest, workspace, self string, fetched bool) (map[string]string, error) {
	sources := make(map[string]string, len(m.Components))
	for _, component := range m.Components {
		if fetched && component.Revision != "self" {
			sources[component.Name] = managedSourcePath(workspace, component)
		} else {
			sources[component.Name] = filepath.Join(workspace, component.Name)
		}
	}
	if strings.TrimSpace(self) != "" {
		path, err := filepath.Abs(self)
		if err != nil {
			return nil, fmt.Errorf("resolve Bench source: %w", err)
		}
		sources["bench"] = path
	}
	return sources, nil
}

func managedSourcePath(workspace string, component suite.Component) string {
	return filepath.Join(workspace, component.Name+"-"+component.Revision)
}

func fetchMissing(m suite.Manifest, workspace string, sources map[string]string) error {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return fmt.Errorf("create source workspace: %w", err)
	}
	for _, component := range m.Components {
		target := sources[component.Name]
		_, err := os.Stat(target)
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect %s source: %w", component.Name, err)
		}
		if component.Revision == "self" {
			return fmt.Errorf("%s source is missing at %s; the component with revision self must be the checkout running benchpack", component.Name, target)
		}
		if target != managedSourcePath(workspace, component) {
			return fmt.Errorf("cannot fetch %s into explicit source path %s", component.Name, target)
		}
		if err := fetchComponent(workspace, component); err != nil {
			return err
		}
	}
	return nil
}

func fetchComponent(workspace string, component suite.Component) error {
	stage, err := os.MkdirTemp(workspace, ".benchpack-fetch-"+component.Name+"-")
	if err != nil {
		return fmt.Errorf("stage %s source: %w", component.Name, err)
	}
	defer os.RemoveAll(stage)
	checkout := filepath.Join(stage, component.Name)
	if err := command("", "git", "clone", "--quiet", "--no-checkout", "--filter=blob:none", component.Repository, checkout); err != nil {
		return fmt.Errorf("clone %s: %w", component.Name, err)
	}
	if err := command("", "git", "-C", checkout, "fetch", "--quiet", "--depth=1", "origin", component.Revision); err != nil {
		return fmt.Errorf("fetch %s revision %s: %w", component.Name, component.Revision, err)
	}
	if err := command("", "git", "-C", checkout, "checkout", "--quiet", "--detach", component.Revision); err != nil {
		return fmt.Errorf("check out %s revision %s: %w", component.Name, component.Revision, err)
	}
	target := managedSourcePath(workspace, component)
	if err := os.Rename(checkout, target); err != nil {
		return fmt.Errorf("install %s source: %w", component.Name, err)
	}
	return nil
}

func command(dir, path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

// runnableTools returns binaries that can run on the packaging host. For a
// cross-target archive it builds temporary host copies from the same pinned
// sources, so version checks and generated references do not execute foreign
// binaries.
func runnableTools(root, stage string, sources map[string]string, components []suite.Component, o options) (string, error) {
	if o.GOOS != runtime.GOOS || o.GOARCH != runtime.GOARCH {
		toolDir := filepath.Join(stage, ".host-tools")
		if err := os.MkdirAll(toolDir, 0o755); err != nil {
			return "", err
		}
		host := o
		host.GOOS, host.GOARCH = runtime.GOOS, runtime.GOARCH
		for _, component := range components {
			if component.Kind != "go" {
				continue
			}
			if err := buildGo(sources[component.Name], filepath.Join(toolDir, executable(component.Name, runtime.GOOS)), host); err != nil {
				return "", fmt.Errorf("build host %s for bundle verification: %w", component.Name, err)
			}
		}
		return toolDir, nil
	}
	return filepath.Join(root, "bin"), nil
}

func verifyVersions(root, toolDir string, m suite.Manifest) error {
	for _, component := range m.Components {
		path := filepath.Join(toolDir, executable(component.Name, runtime.GOOS))
		if component.Kind == "files" {
			path = filepath.Join(root, "bin", component.Name)
		}
		cmd := exec.Command(path, "version")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("read %s version: %w: %s", component.Name, err, strings.TrimSpace(string(output)))
		}
		want := component.Name + " " + component.Version
		if got := strings.TrimSpace(string(output)); got != want {
			return fmt.Errorf("%s reports %q; suite manifest requires %q", component.Name, got, want)
		}
	}
	return nil
}

type moduleInventory struct {
	Schema     int                  `json:"schema"`
	Suite      string               `json:"suite"`
	Target     inventoryTarget      `json:"target"`
	Components []componentInventory `json:"components"`
}

type inventoryTarget struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type componentInventory struct {
	Name       string         `json:"name"`
	GoVersion  string         `json:"go_version"`
	Main       moduleRecord   `json:"main"`
	Modules    []moduleRecord `json:"modules,omitempty"`
	BuildFlags []buildFlag    `json:"build_flags,omitempty"`
}

type moduleRecord struct {
	Path    string        `json:"path"`
	Version string        `json:"version,omitempty"`
	Sum     string        `json:"sum,omitempty"`
	Replace *moduleRecord `json:"replace,omitempty"`
}

type buildFlag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func writeGoModules(root string, m suite.Manifest, o options) (moduleInventory, error) {
	inventory := moduleInventory{
		Schema: 1,
		Suite:  m.Version,
		Target: inventoryTarget{OS: o.GOOS, Arch: o.GOARCH},
	}
	for _, component := range m.Components {
		if component.Kind != "go" {
			continue
		}
		path := filepath.Join(root, "bin", executable(component.Name, o.GOOS))
		info, err := buildinfo.ReadFile(path)
		if err != nil {
			return moduleInventory{}, fmt.Errorf("read %s Go build info: %w", component.Name, err)
		}
		record := componentInventory{Name: component.Name, GoVersion: info.GoVersion, Main: moduleFrom(&info.Main)}
		for _, dependency := range info.Deps {
			record.Modules = append(record.Modules, moduleFrom(dependency))
		}
		sort.Slice(record.Modules, func(i, j int) bool { return record.Modules[i].Path < record.Modules[j].Path })
		for _, setting := range info.Settings {
			record.BuildFlags = append(record.BuildFlags, buildFlag{Key: setting.Key, Value: setting.Value})
		}
		sort.Slice(record.BuildFlags, func(i, j int) bool { return record.BuildFlags[i].Key < record.BuildFlags[j].Key })
		inventory.Components = append(inventory.Components, record)
	}
	data, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return moduleInventory{}, err
	}
	if err := os.WriteFile(filepath.Join(root, "go-modules.json"), append(data, '\n'), 0o644); err != nil {
		return moduleInventory{}, err
	}
	return inventory, nil
}

func moduleFrom(module *runtimedebug.Module) moduleRecord {
	if module == nil {
		return moduleRecord{}
	}
	record := moduleRecord{Path: module.Path, Version: module.Version, Sum: module.Sum}
	if module.Replace != nil {
		replacement := moduleFrom(module.Replace)
		record.Replace = &replacement
	}
	return record
}

func smokeBundle(root string, o options) error {
	if o.GOOS != runtime.GOOS || o.GOARCH != runtime.GOARCH {
		return nil
	}
	temp, err := os.MkdirTemp("", "benchpack-smoke-")
	if err != nil {
		return fmt.Errorf("create bundle smoke directory: %w", err)
	}
	defer os.RemoveAll(temp)
	bench := filepath.Join(root, "bin", executable("bench", runtime.GOOS))
	env := []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + temp,
		"TMPDIR=" + temp,
		"BENCH_DIR=" + filepath.Join(temp, "bench-data"),
		"NO_COLOR=1",
	}
	for _, probe := range []struct {
		name string
		args []string
	}{
		{"Ask", []string{"ask", "-C", root, "-f", filepath.Join(temp, "ask.jsonl"), "suite smoke"}},
		{"Ply", []string{"run", "-C", root, "-f", filepath.Join(temp, "ply.jsonl"), "suite smoke"}},
	} {
		cmd := exec.Command(bench, probe.args...)
		cmd.Env = env
		output, runErr := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(output), "no model") {
			return fmt.Errorf("bundled %s discovery smoke failed: exit=%v output=%q", probe.name, runErr, strings.TrimSpace(string(output)))
		}
	}

	agentHome := filepath.Join(temp, "agent-home")
	agent := filepath.Join(root, "bin", "agent")
	cmd := exec.Command(agent, "new", agentHome, "suite smoke")
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bundled Agent scaffold smoke failed: exit=%v output=%q", err, strings.TrimSpace(string(output)))
	}
	if err := os.WriteFile(filepath.Join(agentHome, "bin", "check"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		return fmt.Errorf("prepare bundled Agent smoke home: %w", err)
	}
	for _, probe := range []struct {
		name string
		args []string
		want string
	}{
		{"check", []string{"home", "check", agentHome}, "valid agent home"},
		{"zero-model run", []string{"home", "run", agentHome}, "nothing to do"},
		{"Trail history", []string{"home", "history", agentHome}, "agent: history · home="},
	} {
		cmd := exec.Command(bench, probe.args...)
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), probe.want) {
			return fmt.Errorf("bundled Agent %s smoke failed: exit=%v output=%q", probe.name, err, strings.TrimSpace(string(output)))
		}
	}
	runs, err := os.ReadDir(filepath.Join(agentHome, ".agent", "runs"))
	if err != nil {
		return fmt.Errorf("inspect bundled Agent smoke evidence: %w", err)
	}
	if len(runs) != 0 {
		return fmt.Errorf("bundled Agent zero-model smoke created %d run artifact(s)", len(runs))
	}
	return nil
}

func componentVersion(m suite.Manifest, name string) (string, error) {
	for _, component := range m.Components {
		if component.Name == name {
			return component.Version, nil
		}
	}
	return "", fmt.Errorf("suite has no %s component", name)
}

func smokeInstall(root string, m suite.Manifest, o options) error {
	if o.GOOS != runtime.GOOS || o.GOARCH != runtime.GOARCH {
		return nil
	}
	temp, err := os.MkdirTemp("", "benchpack-install-")
	if err != nil {
		return fmt.Errorf("create install smoke directory: %w", err)
	}
	defer os.RemoveAll(temp)
	prefix := filepath.Join(temp, "prefix")
	installer := filepath.Join(root, "install.sh")
	for attempt := 1; attempt <= 2; attempt++ {
		cmd := exec.Command(installer, prefix)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + temp, "TMPDIR=" + temp}
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("bundle install smoke attempt %d: %w: %s", attempt, err, strings.TrimSpace(string(output)))
		}
	}
	for _, component := range m.Components {
		cmd := exec.Command(filepath.Join(prefix, "bin", component.Name), "version")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + temp, "TMPDIR=" + temp}
		output, err := cmd.CombinedOutput()
		want := component.Name + " " + component.Version
		if err != nil || strings.TrimSpace(string(output)) != want {
			return fmt.Errorf("installed %s smoke failed: exit=%v output=%q; want %q",
				component.Name, err, strings.TrimSpace(string(output)), want)
		}
	}
	blockedPrefix := filepath.Join(temp, "blocked-prefix")
	blockedBench := filepath.Join(blockedPrefix, "bin", "bench")
	if err := os.MkdirAll(filepath.Dir(blockedBench), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(blockedBench, []byte("owned by someone else\n"), 0o755); err != nil {
		return err
	}
	blocked := exec.Command(installer, blockedPrefix)
	blocked.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + temp, "TMPDIR=" + temp}
	if output, err := blocked.CombinedOutput(); err == nil || !strings.Contains(string(output), "refusing to replace non-symlink") {
		return fmt.Errorf("installer overwrite refusal smoke failed: exit=%v output=%q", err, strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(blockedBench)
	if err != nil || string(data) != "owned by someone else\n" {
		return fmt.Errorf("installer changed unrelated command: %w", err)
	}
	blockedMayPrefix := filepath.Join(temp, "blocked-may-prefix")
	blockedMay := filepath.Join(blockedMayPrefix, "bin", "may")
	if err := os.MkdirAll(filepath.Dir(blockedMay), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(blockedMay, []byte("another may\n"), 0o755); err != nil {
		return err
	}
	blocked = exec.Command(installer, blockedMayPrefix)
	blocked.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + temp, "TMPDIR=" + temp}
	if output, err := blocked.CombinedOutput(); err == nil || !strings.Contains(string(output), "refusing to replace non-symlink "+blockedMay) {
		return fmt.Errorf("installer May overwrite refusal smoke failed: exit=%v output=%q", err, strings.TrimSpace(string(output)))
	}
	data, err = os.ReadFile(blockedMay)
	if err != nil || string(data) != "another may\n" {
		return fmt.Errorf("installer changed unrelated May command: %w", err)
	}
	return nil
}

// syncDraft makes its generated tool reference describe the suite being
// packaged, not whichever optional tools happened to be installed on the
// release runner.
func syncDraft(root, stage, toolDir string) error {
	draft := filepath.Join(root, "bin", "draft")
	missingOptional := filepath.Join(stage, ".not-in-base-suite")
	cmd := exec.Command(draft, "sync")
	cmd.Dir = root
	cmd.Env = overlay(os.Environ(),
		"ASK="+filepath.Join(toolDir, executable("ask", runtime.GOOS)),
		"BRIEF="+filepath.Join(toolDir, executable("brief", runtime.GOOS)),
		"PLY="+filepath.Join(toolDir, executable("ply", runtime.GOOS)),
		"HONE="+filepath.Join(toolDir, executable("hone", runtime.GOOS)),
		"CAGE="+missingOptional,
		"MAY="+missingOptional,
		"VOUCH="+missingOptional,
		"WEB="+missingOptional,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sync Draft tool reference: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func resolve(m suite.Manifest, sources map[string]string, allowDirty bool) (suite.Manifest, bool, error) {
	anyDirty := false
	for i, component := range m.Components {
		source := sources[component.Name]
		info, err := os.Stat(source)
		if err != nil || !info.IsDir() {
			return suite.Manifest{}, false, fmt.Errorf("%s source is not a directory at %s", component.Name, source)
		}
		head, err := git(source, "rev-parse", "HEAD")
		if err != nil {
			return suite.Manifest{}, false, fmt.Errorf("inspect %s revision: %w", component.Name, err)
		}
		if component.Revision != "self" && head != component.Revision {
			return suite.Manifest{}, false, fmt.Errorf("%s is at %s; suite requires %s", component.Name, head, component.Revision)
		}
		status, err := git(source, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return suite.Manifest{}, false, fmt.Errorf("inspect %s worktree: %w", component.Name, err)
		}
		component.Revision = head
		component.Dirty = status != ""
		if component.Dirty && !allowDirty {
			return suite.Manifest{}, false, fmt.Errorf("%s worktree is dirty; commit it or use -allow-dirty for a non-release build", component.Name)
		}
		anyDirty = anyDirty || component.Dirty
		m.Components[i] = component
	}
	return m, anyDirty, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func buildGo(source, target string, o options) error {
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", target, ".")
	cmd.Dir = source
	cmd.Env = overlay(os.Environ(), "GOOS="+o.GOOS, "GOARCH="+o.GOARCH, "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func overlay(base []string, values ...string) []string {
	result := append([]string(nil), base...)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		prefix := key + "="
		kept := result[:0]
		for _, item := range result {
			if !strings.HasPrefix(item, prefix) {
				kept = append(kept, item)
			}
		}
		result = append(kept, value)
	}
	return result
}

func executable(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse non-regular asset %s", path)
		}
		return copyFile(path, destination, info.Mode().Perm())
	})
}

func copyFile(source, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

func writeChecksums(root string) error {
	var lines []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in bundle: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(lines)
	return os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func writeArchive(target, stage, name string) (resultErr error) {
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	gz := gzip.NewWriter(file)
	gz.Name = ""
	gz.ModTime = time.Unix(0, 0)
	defer func() { resultErr = errors.Join(resultErr, gz.Close()) }()
	tw := tar.NewWriter(gz)
	defer func() { resultErr = errors.Join(resultErr, tw.Close()) }()
	root := filepath.Join(stage, name)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("refuse non-regular bundle member %s", path)
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
		}
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
}

func writeArchiveChecksum(archive string) error {
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  " + filepath.Base(archive) + "\n"
	return os.WriteFile(archive+".sha256", []byte(line), 0o644)
}

func installText(name string) string {
	return fmt.Sprintf(`# Bench suite

This directory is a complete, pinned Bench runtime. No Go toolchain is needed.

Install it for the current user (default prefix: $HOME/.local):

    cd %s
    ./install.sh

Or run it without installing:

    cd %s
    export PATH="$PWD/bin:$PATH"
    bench version

An application may keep the directory private and invoke `+"`bin/bench`"+` by
absolute path. Bench discovers the other suite programs beside its executable;
the application does not need to rewrite the user's PATH.

`+"`suite.json`"+` records the exact source revision and standalone version of
every component. `+"`SHA256SUMS`"+` covers every other shipped file.
`, name, name)
}

func componentNames(components []suite.Component) []string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, component.Name)
	}
	return names
}

func installScript(version string, tools []string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu

version=%s
here=$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)
prefix=${1:-${HOME:+$HOME/.local}}
[ -n "$prefix" ] || { echo "install.sh: pass a prefix or set HOME" >&2; exit 2; }
case $prefix in /*) ;; *) prefix=$PWD/$prefix ;; esac
lib=$prefix/lib/bench-suite
dest=$lib/$version
bindir=$prefix/bin
tools='%s'

verify() {
	dir=$1
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$dir" && sha256sum -c SHA256SUMS >/dev/null)
	elif command -v shasum >/dev/null 2>&1; then
		(cd "$dir" && shasum -a 256 -c SHA256SUMS >/dev/null)
	else
		echo "install.sh: need sha256sum or shasum to verify the suite" >&2
		return 1
	fi
}

verify "$here" || { echo "install.sh: source suite checksum failed" >&2; exit 1; }

for tool in $tools; do
	link=$bindir/$tool
	if [ -e "$link" ] || [ -L "$link" ]; then
		if [ ! -L "$link" ]; then
			echo "install.sh: refusing to replace non-symlink $link" >&2
			exit 1
		fi
		target=$(readlink "$link")
		case $target in "$lib"/*/bin/"$tool") ;; *)
			echo "install.sh: refusing to replace unrelated symlink $link -> $target" >&2
			exit 1
		;; esac
	fi
done

mkdir -p "$lib" "$bindir"
if [ "$here" != "$dest" ]; then
	if [ -d "$dest" ]; then
		verify "$dest" || { echo "install.sh: existing $dest is incomplete" >&2; exit 1; }
		cmp -s "$here/suite.json" "$dest/suite.json" || {
			echo "install.sh: $dest contains a different build of suite $version" >&2
			exit 1
		}
		cmp -s "$here/SHA256SUMS" "$dest/SHA256SUMS" || {
			echo "install.sh: $dest contains different files for suite $version" >&2
			exit 1
		}
	else
		stage=$(mktemp -d "$lib/.install.XXXXXX")
		cleanup() { [ -z "${stage:-}" ] || rm -rf -- "$stage"; }
		trap cleanup EXIT HUP INT TERM
		cp -R "$here/." "$stage/"
		mv "$stage" "$dest"
		stage=
		trap - EXIT HUP INT TERM
	fi
fi

for tool in $tools; do
	ln -sfn "$dest/bin/$tool" "$bindir/$tool"
done

echo "Bench suite $version installed in $dest"
case :${PATH:-}: in *:"$bindir":*) ;; *)
	echo "Add $bindir to PATH." >&2
;; esac
`, version, strings.Join(tools, " "))
}

func report(w io.Writer, err error) int {
	fmt.Fprintln(w, "benchpack:", err)
	return 1
}
