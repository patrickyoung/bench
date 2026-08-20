// Command fakeask is an offline fixture for manually exercising skilled Ask turns.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "system" {
		fmt.Println("You are a careful fixture model.")
		return
	}
	if len(os.Args) < 5 || os.Args[1] != "-f" {
		os.Exit(1)
	}
	session := os.Args[2]
	i := 3
	system := ""
	if os.Args[i] == "-S" {
		if len(os.Args) < i+4 {
			os.Exit(1)
		}
		system = os.Args[i+1]
		i += 2
	}
	if os.Args[i] != "--" || i+1 >= len(os.Args) {
		os.Exit(1)
	}
	if err := os.WriteFile(session, []byte("{}\n"), 0o600); err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(session+".system", []byte(system), 0o600); err != nil {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "ask: selected procedure recorded in the request")
	fmt.Println("I’ll follow the active brief: name the claim, run its executable fixture, and refuse unsupported findings.")
}
