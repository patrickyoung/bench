// Command fakehone is an offline fixture for manually exercising bench's
// public hone boundary. It is intentionally not part of the application.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 4 || os.Args[1] != "-into" {
		os.Exit(2)
	}
	skill, session := os.Args[2], os.Args[3]
	if _, err := os.Stat(session); err != nil {
		fmt.Fprintln(os.Stderr, "hone:", err)
		os.Exit(2)
	}
	dir := filepath.Join(".fixture-skills", skill)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		os.Exit(2)
	}
	body := "---\nname: " + skill + "\ndescription: Lessons from verified fixture builds.\n---\n\n## Lessons\n\n- Keep the executable check beside the agent.\n"
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "hone: fixture-build: 2 stumbles, check passed")
	fmt.Fprintln(os.Stderr, "hone:", path+": 1 lesson(s) added (1 total)")
}
