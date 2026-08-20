// Command fakeply is an offline fixture for manually exercising skill refinement.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 5 || os.Args[1] != "-sh" || os.Args[2] != "-check" {
		os.Exit(1)
	}
	source, err := io.ReadAll(os.Stdin)
	if err != nil || len(source) == 0 {
		os.Exit(2)
	}
	cwd, _ := os.Getwd()
	name := filepath.Base(cwd)
	body := "---\nname: " + name + "\ndescription: Apply evidence-backed review procedure. Use when reviewing patches or adding repeatable checks.\n---\n\n# " + name + "\n\n## Source distilled\n\n" + string(source) + "\n\n## Steps\n\n1. Read the change and name the claim.\n2. Run the executable fixture that can disprove it.\n3. Report only findings supported by the result.\n\n## Rules\n\n- Refuse claims that cannot be checked offline.\n"
	if err := os.WriteFile(filepath.Join(cwd, "SKILL.md"), []byte(body), 0o600); err != nil {
		os.Exit(1)
	}
	sessions := os.Getenv("PLY_DIR")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(sessions, "fixture-refine.jsonl"), []byte("{}\n"), 0o600); err != nil {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "$ edit SKILL.md from spooled stdin")
	fmt.Fprintln(os.Stderr, "$ brief lint -strict .")
	fmt.Fprintln(os.Stderr, "brief: 1 skill(s), 0 error(s), 0 warning(s)")
	fmt.Println("Refined the skill; the strict check passed.")
}
