package providers

import "fmt"

// The mcpServers block many clients share. Use it for a runner with no entry of
// its own, and check its docs for whether it spells the transport "http" or
// "streamable-http".
func init() {
	register(Provider{
		Name:  "generic",
		Title: "generic mcpServers block",
		Path:  "the file your client reads mcpServers from",
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
	})
}
