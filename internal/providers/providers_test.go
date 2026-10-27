package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

var target = Target{
	Name:  "mcp-linux-go",
	URL:   "http://127.0.0.1:3000/mcp",
	Token: "0123456789abcdef0123456789abcdef",
}

func TestDefaultIsRegistered(t *testing.T) {
	if _, ok := Lookup(Default); !ok {
		t.Fatalf("default provider %q is not registered", Default)
	}
}

func TestEveryProviderCarriesURLAndToken(t *testing.T) {
	for _, name := range Names() {
		p, _ := Lookup(name)
		out := Config(p, target)
		for _, want := range []string{target.URL, target.Token, target.Name} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: config does not carry %q", name, want)
			}
		}
	}
}

// Every provider but openclaw emits strict JSON; openclaw's config is JSON5,
// which has trailing commas and bare keys.
func TestJSONProvidersEmitValidJSON(t *testing.T) {
	for _, name := range Names() {
		if name == "openclaw" {
			continue
		}
		p, _ := Lookup(name)
		var v any
		if err := json.Unmarshal([]byte(p.Render(target)), &v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown provider resolved")
	}
}
