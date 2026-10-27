package providers

import "fmt"

// Shapes follow Mcp.RemoteConfigEncoded in https://opencode.ai/config.json.
// protocol "auto" probes for 2026-07-28 and falls back to the handshake, both
// of which this server answers.
func init() {
	register(Provider{
		Name:  "opencode",
		Title: "opencode",
		Path:  "opencode.json (project root)",
		Render: func(t Target) string {
			return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      %q: {
        "type": "remote",
        "url": %q,
        "headers": {
          "Authorization": "Bearer %s"
        },
        "protocol": "auto"
      }
    }
  }
}
`, t.Name, t.URL, t.Token)
		},
	})
}
