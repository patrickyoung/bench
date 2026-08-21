// Command bench is the terminal workbench for the bench filter family.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/briefexec"
	"github.com/patrickyoung/bench/internal/draftexec"
	"github.com/patrickyoung/bench/internal/honeexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
	"github.com/patrickyoung/bench/internal/ui"
)

const version = "0.3.0"

func main() {
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: bench [-new | -session id-or-path | -project dir] [initial task]")
		fmt.Fprintln(flag.CommandLine.Output(), "\nA terminal workbench over the bench Unix filters.")
		flag.PrintDefaults()
	}
	showVersion := flag.Bool("version", false, "print version and exit")
	resume := flag.String("session", "", "verify and resume a session id or path")
	startNew := flag.Bool("new", false, "start new instead of showing saved sessions")
	project := flag.String("project", "", "open an existing agent project and recheck its DESIGN.md")
	flag.Parse()
	if *showVersion {
		fmt.Println("bench " + version)
		return
	}
	modeCount := 0
	for _, selected := range []bool{*startNew, *resume != "", *project != ""} {
		if selected {
			modeCount++
		}
	}
	if modeCount > 1 {
		fatal(fmt.Errorf("-new, -session, and -project cannot be used together"))
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	root := os.Getenv("BENCH_DIR")
	if root == "" {
		root = filepath.Join(cwd, ".bench")
	}
	askPath := os.Getenv("BENCH_ASK")
	if askPath == "" {
		askPath = "ask"
	}
	draftPath := os.Getenv("BENCH_DRAFT")
	if draftPath == "" {
		draftPath = "draft"
	}
	honePath := os.Getenv("BENCH_HONE")
	if honePath == "" {
		honePath = "hone"
	}
	briefPath := os.Getenv("BENCH_BRIEF")
	if briefPath == "" {
		briefPath = "brief"
	}
	plyPath := os.Getenv("BENCH_PLY")
	if plyPath == "" {
		plyPath = "ply"
	}
	toolbox := os.Getenv("BENCH_TOOLS")
	modelName := os.Getenv("ASK_MODEL")
	if modelName == "" {
		modelName = "ask default"
	}
	sessionsDir := filepath.Join(root, "sessions")
	saved, err := session.Discover(sessionsDir)
	if err != nil {
		fatal(err)
	}
	initial := strings.Join(flag.Args(), " ")
	if *project != "" && initial != "" {
		fatal(fmt.Errorf("initial task cannot be combined with -project"))
	}
	projectDir := ""
	if *project != "" {
		projectDir, err = ui.ProjectPath(cwd, *project)
		if err != nil {
			fatal(err)
		}
	}
	newPath := filepath.Join(sessionsDir, sessionName())
	active := newPath
	resuming := *resume != ""
	if resuming {
		active = session.Resolve(sessionsDir, *resume)
	}

	plyRunner := plyexec.Runner{Path: plyPath, AskPath: askPath, BriefPath: briefPath}
	m := ui.New(ui.Config{
		Runner:        askexec.Runner{Path: askPath, BriefPath: briefPath},
		Task:          plyRunner,
		Draft:         draftexec.Runner{Path: draftPath, WorkDir: cwd},
		Hone:          honeexec.Runner{Path: honePath, WorkDir: cwd},
		Brief:         briefexec.Runner{Binary: briefPath, WorkDir: cwd},
		Ply:           plyRunner,
		Session:       active,
		NewSession:    newPath,
		Resume:        resuming,
		Choose:        !*startNew && !resuming && projectDir == "" && initial == "" && len(saved) > 0,
		Sessions:      saved,
		Model:         modelName,
		Workspace:     cwd,
		DataDir:       root,
		Project:       projectDir,
		InitialPrompt: initial,
		Toolbox:       toolbox,
	})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fatal(err)
	}
}

func sessionName() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return time.Now().Format("20060102-150405.000000000") + ".jsonl"
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:]) + ".jsonl"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bench:", err)
	os.Exit(1)
}
