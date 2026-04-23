// Package validator ports tumf/mcp-shell-server allowlist + argv validation.
//
// Rules:
//  1. ALLOW_COMMANDS (alias: ALLOWED_COMMANDS) = comma-separated list, spaces trimmed.
//  2. Empty/unset list -> UNRESTRICTED mode (caller warns on boot).
//  3. Command is argv []string. Shell operators `;`, `&&`, `||`, `|` may appear as
//     separate argv tokens. We never shell-interpret.
//  4. Split argv on operator-tokens. Each segment's head (argv[0]) must be allowed.
//  5. Empty segment (e.g. ["ls", ";", ";", "cat"]) -> invalid.
//  6. If head token contains an operator char, reject (defense in depth).
package validator

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var shellOperators = map[string]struct{}{
	";":  {},
	"&&": {},
	"||": {},
	"|":  {},
}

var operatorCharRE = regexp.MustCompile(`[;|&]`)

// IsOperator reports whether tok is one of `;`, `&&`, `||`, `|`.
func IsOperator(tok string) bool {
	_, ok := shellOperators[tok]
	return ok
}

// AllowList is the parsed allowlist config.
type AllowList struct {
	Unrestricted bool
	Commands     map[string]struct{}
}

// Load parses a comma-separated allowlist string. Empty -> unrestricted.
func Load(raw string) AllowList {
	cmds := map[string]struct{}{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			cmds[s] = struct{}{}
		}
	}
	return AllowList{Unrestricted: len(cmds) == 0, Commands: cmds}
}

// Sorted returns allowlist commands sorted alphabetically.
func (a AllowList) Sorted() []string {
	out := make([]string, 0, len(a.Commands))
	for c := range a.Commands {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// SplitOnOperators splits argv on operator tokens. Returns nil if any segment is empty.
func SplitOnOperators(argv []string) [][]string {
	var segments [][]string
	var current []string
	for _, tok := range argv {
		if IsOperator(tok) {
			if len(current) == 0 {
				return nil
			}
			segments = append(segments, current)
			current = nil
		} else {
			current = append(current, tok)
		}
	}
	if len(current) == 0 {
		return nil
	}
	segments = append(segments, current)
	return segments
}

// Validate argv against allowlist. Returns nil on success.
func Validate(argv []string, allow AllowList) error {
	if len(argv) == 0 {
		return fmt.Errorf("Empty command")
	}
	segments := SplitOnOperators(argv)
	if segments == nil {
		return fmt.Errorf("Invalid shell operator placement (empty segment)")
	}
	for _, seg := range segments {
		head := seg[0]
		if operatorCharRE.MatchString(head) {
			return fmt.Errorf("Command contains shell operator: %s", head)
		}
		if !allow.Unrestricted {
			if _, ok := allow.Commands[head]; !ok {
				return fmt.Errorf("Command not allowed: %s", head)
			}
		}
	}
	return nil
}
