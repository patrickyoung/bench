// Command fakebrief is an offline fixture for manually exercising the Skills UI.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ls":
		if len(os.Args) == 2 {
			fmt.Println("go-review\tReview Go patches with executable fixtures and explicit refusals.")
			fmt.Println("incident-notes\tTurn incident evidence into a repeatable diagnostic procedure.")
			entries, _ := os.ReadDir(filepath.Join(".claude", "skills"))
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != "go-review" && entry.Name() != "incident-notes" {
					fmt.Printf("%s\tProject skill built from source material.\n", entry.Name())
				}
			}
			return
		}
		fmt.Println("SKILL.md")
	case "path":
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		cwd, _ := os.Getwd()
		fmt.Println(filepath.Join(cwd, ".claude", "skills", os.Args[2]))
	case "cat":
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		ref := os.Args[2]
		if !strings.ContainsAny(ref, "/.") {
			ref = filepath.Join(".claude", "skills", ref, "SKILL.md")
		}
		body, err := os.ReadFile(ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "brief:", err)
			os.Exit(2)
		}
		fmt.Print(string(body))
	case "lint":
		if len(os.Args) != 4 || os.Args[2] != "-strict" {
			os.Exit(2)
		}
		if _, err := os.Stat(filepath.Join(os.Args[3], "SKILL.md")); err != nil {
			fmt.Fprintln(os.Stderr, "brief:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "brief: 1 skill(s), 0 error(s), 0 warning(s)")
	case "new":
		if len(os.Args) != 5 || os.Args[2] != "-d" {
			os.Exit(2)
		}
		dir := filepath.Join(os.Args[3], os.Args[4])
		if err := os.MkdirAll(dir, 0o700); err != nil {
			os.Exit(2)
		}
		body := "---\nname: " + os.Args[4] + "\ndescription: Fixture scaffold. Use when authoring this fixture skill.\n---\n\n# Steps\n\n1. Replace this scaffold from source.\n"
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			os.Exit(2)
		}
		fmt.Println(path)
	default:
		os.Exit(2)
	}
}
