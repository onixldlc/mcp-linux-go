// Package providers renders this server's connection details into the config
// shape each agent runner reads. Translation only — no provider changes how the
// server behaves.
package providers

import (
	"fmt"
	"sort"
	"strings"
)

// Default is used when no provider is selected.
const Default = "claudecode"

// Target is the running server, as a client has to address it.
type Target struct {
	Name  string
	URL   string
	Token string
}

type Provider struct {
	Name  string
	Title string
	// Path is where the runner reads this config from.
	Path string
	// Render returns the config block to paste into Path.
	Render func(Target) string
	// CLI returns an equivalent command, when the runner has one.
	CLI func(Target) string
}

var registry = map[string]Provider{}

func register(p Provider) { registry[p.Name] = p }

func Lookup(name string) (Provider, bool) {
	p, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// List is one line per provider, for --list-providers.
func List() string {
	var b strings.Builder
	for _, n := range Names() {
		p := registry[n]
		marker := " "
		if n == Default {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %-12s %-28s %s\n", marker, p.Name, p.Title, p.Path)
	}
	fmt.Fprintf(&b, "\n* default\n")
	return b.String()
}

// Snippet is the config block plus the command alternative, if there is one.
func Snippet(p Provider, t Target) string {
	var b strings.Builder
	b.WriteString(p.Render(t))
	if p.CLI != nil {
		fmt.Fprintf(&b, "\n# or:\n%s\n", p.CLI(t))
	}
	return b.String()
}

// Config is Snippet with a line naming the provider and where its config lives.
func Config(p Provider, t Target) string {
	return fmt.Sprintf("# %s — %s\n%s", p.Title, p.Path, Snippet(p, t))
}
