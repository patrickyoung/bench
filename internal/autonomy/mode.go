// Package autonomy names Bench's user-facing delegation levels. It keeps
// product language separate from the lower-level contract and Ply switches.
package autonomy

import (
	"fmt"
	"strings"
)

type Mode string

const (
	Quick  Mode = "quick"
	Review Mode = "review"
	Loop   Mode = "loop"
)

func Parse(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case Quick:
		return Quick, nil
	case Review, "":
		return Review, nil
	case Loop:
		return Loop, nil
	default:
		return "", fmt.Errorf("autonomy mode %q is not supported (use quick, review, or loop)", value)
	}
}

func FromContract(enabled bool) Mode {
	if enabled {
		return Review
	}
	return Quick
}

func FromPolicy(contract, loop bool) Mode {
	if !contract {
		return Quick
	}
	if loop {
		return Loop
	}
	return Review
}

func (m Mode) UsesContract() bool { return m != Quick }

func (m Mode) Description() string {
	if m == Quick {
		return "skip contract review and start Ply with the current tool grant"
	}
	if m == Loop {
		return "review once, then let one bounded Ply invocation pursue the configured verifier"
	}
	return "negotiate and admit a durable outcome before work starts"
}
