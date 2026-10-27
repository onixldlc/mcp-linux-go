package providers

import "fmt"

// OpenClaw's config is JSON5 and validated strictly: unknown keys stop the
// Gateway from starting. Remote entries key off transport, not type.
func init() {
	register(Provider{
		Name:  "openclaw",
		Title: "OpenClaw",
		Path:  "~/.openclaw/openclaw.json (JSON5)",
		Render: func(t Target) string {
			return fmt.Sprintf(`{
  mcp: {
    servers: {
      %q: {
        url: %q,
        transport: "streamable-http",
        headers: {
          Authorization: "Bearer %s",
        },
        enabled: true,
      },
    },
  },
}
`, t.Name, t.URL, t.Token)
		},
		CLI: func(t Target) string {
			return fmt.Sprintf("openclaw mcp add %s --url %s --transport streamable-http\n"+
				"# then add the Authorization header with `openclaw mcp set` or in the config above",
				t.Name, t.URL)
		},
	})
}
