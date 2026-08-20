// Command fakedraft is an offline fixture for manually exercising bench's
// public draft boundary. It is intentionally not part of the application.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	dir := os.Args[2]
	switch os.Args[1] {
	case "new":
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "draft:", err)
			os.Exit(2)
		}
		body := "# Fixture agent\n\n## Requirements\n\n- [x] " + os.Args[3] +
			"\n\n## The split\n\n| stage | tool | why |\n| --- | --- | --- |\n| decide | ask | judgment |\n" +
			"\n## Not doing\n\n- No hidden runtime.\n\n## Check\n\n```sh\n./bin/check\n```\n"
		if err := os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte(body), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "draft:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "draft: wrote DESIGN.md from fixture requirements")
		fmt.Println(filepath.Join(dir, "DESIGN.md"))
	case "check":
		body, err := os.ReadFile(filepath.Join(dir, "DESIGN.md"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "draft:", err)
			os.Exit(2)
		}
		if strings.Contains(string(body), "```sh\nfalse\n```") {
			fmt.Fprintln(os.Stderr, "draft: check is still false")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "draft: DESIGN.md is buildable")
		fmt.Println("./bin/check")
	case "build":
		fmt.Fprintln(os.Stderr, "$ printf '#!/bin/sh' > bin/agent")
		fmt.Fprintln(os.Stderr, "exit 0")
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", "agent"), []byte("#!/bin/sh\necho fixture-agent\n"), 0o700); err != nil {
			os.Exit(1)
		}
		plyDir := os.Getenv("PLY_DIR")
		if err := os.MkdirAll(plyDir, 0o700); err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(plyDir, "fixture-build.jsonl"), []byte("{}\n"), 0o600); err != nil {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "draft: check passed: ./bin/check")
		fmt.Println("Built the fixture agent and its check passed.")
	case "prove":
		fmt.Fprintln(os.Stderr, "draft: check passes clean; mutating with a 15s bound")
		fmt.Fprintln(os.Stderr, "draft: killed 3 of 3 (100%), 0 survived")
		fmt.Fprintln(os.Stderr, "draft: the check caught every change that was made to break it")
	default:
		os.Exit(2)
	}
}
