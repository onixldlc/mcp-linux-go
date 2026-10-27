package providers

import "fmt"

// Claude Code reads an http entry under mcpServers. An entry with a url but no
// type is read as stdio and skipped, so "type" is not optional here.
func init() {
	register(Provider{
		Name:  "claudecode",
		Title: "Claude Code",
		Path:  ".mcp.json (project) or ~/.claude.json (user)",
		Render: func(t Target) string {
			return fmt.Sprintf(`{
  "mcpServers": {
    %q: {
      "type": "http",
      "url": %q,
      "headers": {
        "Authorization": "Bearer %s"
      }
    }
  }
}
`, t.Name, t.URL, t.Token)
		},
		CLI: func(t Target) string {
			return fmt.Sprintf("claude mcp add --transport http %s %s \\\n  --header \"Authorization: Bearer %s\"",
				t.Name, t.URL, t.Token)
		},
	})
}
