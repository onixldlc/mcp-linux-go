package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onixldlc/mcp-linux-go/internal/tools"
	"github.com/onixldlc/mcp-linux-go/internal/validator"
)

const serverName = "mcp-linux-go"

// serverVersion overridden at build time via:
//   go build -ldflags "-X main.serverVersion=$(git describe --tags --always --dirty)"
var serverVersion = "dev"

// protocolVersion is the newest MCP revision the pinned go-sdk speaks. The SDK
// negotiates down automatically for older clients; this is display only.
// Keep in sync with the go-sdk version in go.mod.
const protocolVersion = "2026-07-28"

// maxRequestBodyBytes must fit the largest tool payload. upload_file_b64 accepts
// tools.MaxFileWrite raw bytes, which inflate ~4/3 under base64, plus JSON framing.
// The SDK default is only 4 MiB, which would reject large uploads outright.
const maxRequestBodyBytes = int64(tools.MaxFileWrite)*4/3 + 8<<20

type config struct {
	host          string
	port          int
	token         string
	allowCommands string
}

func printHelp() {
	fmt.Printf(`%s v%s

Usage: %s [options]

Options:
  -H, --host <addr>              HTTP bind address (default: 127.0.0.1) [env: HOST]
  -p, --port <num>               HTTP port (default: 3000)              [env: PORT]
      --token <str>              Bearer token for HTTP (>=16 chars)     [env: SHELL_MCP_TOKEN]
      --allow-commands <csv>     Comma-sep allowlist. Unset = unrestricted.
                                                                        [env: ALLOW_COMMANDS]
  -h, --help                     Show this help
  -v, --version                  Show version

CLI flags override env vars. Env vars override defaults.

Examples:
  %s --allow-commands "ls,cat,pwd,grep"
  %s -H 127.0.0.1 -p 3000 --token "$(openssl rand -hex 32)"

Generate an HTTP token:
  openssl rand -hex 32
`, serverName, serverVersion, serverName, serverName, serverName)
}

func parseConfig() config {
	fs := flag.NewFlagSet(serverName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		host          string
		hostShort     string
		port          int
		portShort     int
		token         string
		allow         string
		help, helpS   bool
		ver, verS     bool
	)
	fs.StringVar(&host, "host", "", "HTTP bind address")
	fs.StringVar(&hostShort, "H", "", "HTTP bind address (short)")
	fs.IntVar(&port, "port", 0, "HTTP port")
	fs.IntVar(&portShort, "p", 0, "HTTP port (short)")
	fs.StringVar(&token, "token", "", "Bearer token")
	fs.StringVar(&allow, "allow-commands", "", "Comma-sep allowlist")
	fs.BoolVar(&help, "help", false, "Show help")
	fs.BoolVar(&helpS, "h", false, "Show help (short)")
	fs.BoolVar(&ver, "version", false, "Show version")
	fs.BoolVar(&verS, "v", false, "Show version (short)")

	fs.Usage = printHelp
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] arg error: %v\n", serverName, err)
		fmt.Fprintf(os.Stderr, "Run '%s --help' for usage.\n", serverName)
		os.Exit(2)
	}
	if help || helpS {
		printHelp()
		os.Exit(0)
	}
	if ver || verS {
		fmt.Printf("%s v%s\n", serverName, serverVersion)
		os.Exit(0)
	}

	hostVal := firstNonEmpty(host, hostShort, os.Getenv("HOST"), "127.0.0.1")
	portRaw := firstNonZeroInt(port, portShort)
	if portRaw == 0 {
		if env := os.Getenv("PORT"); env != "" {
			if _, err := fmt.Sscanf(env, "%d", &portRaw); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] invalid PORT env: %q\n", serverName, env)
				os.Exit(2)
			}
		}
	}
	if portRaw == 0 {
		portRaw = 3000
	}
	if portRaw < 1 || portRaw > 65535 {
		fmt.Fprintf(os.Stderr, "[%s] invalid port: %d (expected 1..65535)\n", serverName, portRaw)
		os.Exit(2)
	}

	tokenVal := token
	if tokenVal == "" {
		tokenVal = os.Getenv("SHELL_MCP_TOKEN")
	}
	allowVal := allow
	if allowVal == "" {
		allowVal = os.Getenv("ALLOW_COMMANDS")
	}
	if allowVal == "" {
		allowVal = os.Getenv("ALLOWED_COMMANDS")
	}

	return config{host: hostVal, port: portRaw, token: tokenVal, allowCommands: allowVal}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func firstNonZeroInt(xs ...int) int {
	for _, x := range xs {
		if x != 0 {
			return x
		}
	}
	return 0
}

func tokenFingerprint(tok string) string {
	if len(tok) <= 10 {
		return "***"
	}
	return tok[:6] + "…" + tok[len(tok)-4:]
}

func bootBanner(cfg config, allow validator.AllowList) {
	log := func(s string) { fmt.Fprintf(os.Stderr, "[%s] %s\n", serverName, s) }
	host, _ := os.Hostname()
	u, _ := user.Current()
	uname := ""
	if u != nil {
		uname = u.Username
	}
	cwd, _ := os.Getwd()

	log(fmt.Sprintf("v%s", serverVersion))
	log(fmt.Sprintf("transport:   streamable http (stateless)"))
	log(fmt.Sprintf("protocol:    %s — negotiates down for older clients", protocolVersion))
	log(fmt.Sprintf("runtime:     %s on %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	log(fmt.Sprintf("user:        %s@%s", uname, host))
	log(fmt.Sprintf("pid:         %d", os.Getpid()))
	log(fmt.Sprintf("cwd:         %s", cwd))

	if allow.Unrestricted {
		log("allowlist:   ⚠  UNRESTRICTED — any command the process user can run will execute.")
		log(`             Restrict with:  --allow-commands "ls,cat,pwd,grep"   (or ALLOW_COMMANDS env)`)
	} else {
		log("allowlist:   " + strings.Join(allow.Sorted(), ", "))
	}

	log(fmt.Sprintf("bind:        http://%s:%d/mcp", cfg.host, cfg.port))
	log(fmt.Sprintf("health:      http://%s:%d/healthz", cfg.host, cfg.port))
	if cfg.token != "" && len(cfg.token) >= 16 {
		log(fmt.Sprintf("auth:        Bearer token (%d chars, fp %s):", len(cfg.token), tokenFingerprint(cfg.token)))
		log("             " + cfg.token)
		log("             Authorization: Bearer " + cfg.token)
	} else if cfg.token != "" {
		log(fmt.Sprintf("auth:        ⚠  token too short (%d chars, need >=16)", len(cfg.token)))
		log("             Generate one:  openssl rand -hex 32")
		log("             Pass via:      --token <token>   or   SHELL_MCP_TOKEN=<token>")
	} else {
		log("auth:        ⚠  NO TOKEN SET — HTTP transport will refuse to start.")
		log("             Generate one:  openssl rand -hex 32")
		log("             Pass via:      --token <token>   or   SHELL_MCP_TOKEN=<token>")
	}
	if cfg.host == "0.0.0.0" {
		log("             ⚠  bound to 0.0.0.0 — token is your only defense. Prefer 127.0.0.1 + SSH tunnel.")
	}
}

func buildServer(allow validator.AllowList) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)
	tools.Register(s, allow)
	return s
}

func main() {
	cfg := parseConfig()
	allow := validator.Load(cfg.allowCommands)
	bootBanner(cfg, allow)

	if cfg.token == "" || len(cfg.token) < 16 {
		fmt.Fprintf(os.Stderr, "[%s] FATAL: HTTP transport requires a token (>=16 chars).\n", serverName)
		fmt.Fprintf(os.Stderr, "[%s]        Generate one:  openssl rand -hex 32\n", serverName)
		os.Exit(1)
	}

	server := buildServer(allow)
	// Stateless is required to serve protocol 2026-07-28 over HTTP: that revision
	// drops the initialize handshake and session IDs entirely. Without it clients
	// negotiate down to 2025-11-25. This server has no server->client requests,
	// subscriptions, sampling, or roots, so nothing depends on session state.
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		MaxRequestBodyBytes: maxRequestBodyBytes,
	})

	// go-sdk v1.6.0 stopped enabling cross-origin protection by default and
	// deprecated the option field. Wrap the handler ourselves to keep the
	// DNS-rebinding/CSRF protection that v1.5.0 applied implicitly.
	crossOrigin := http.NewCrossOriginProtection()

	protect := func(h http.Handler) http.Handler {
		return crossOrigin.Handler(bearerAuth(cfg.token, h))
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", protect(mcpHandler))
	mux.Handle("/mcp/", protect(mcpHandler))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "name": serverName, "version": serverVersion,
			"protocol_version": protocolVersion, "stateless": true,
		})
	})

	addr := fmt.Sprintf("%s:%d", cfg.host, cfg.port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "[%s] listening on http://%s/mcp\n", serverName, addr)
	fmt.Fprintf(os.Stderr, "[%s] ready — awaiting requests.\n", serverName)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] fatal: %v\n", serverName, err)
		os.Exit(1)
	}
	_ = context.TODO
}

func bearerAuth(token string, next http.Handler) http.Handler {
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
			unauth(w, "")
			return
		}
		got := []byte(h[len(prefix):])
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			unauth(w, "invalid_token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unauth emits an RFC 6750 Bearer challenge alongside the JSON-RPC error. MCP
// clients key off WWW-Authenticate to tell "needs credentials" apart from a
// generic failure; a bare 401 body leaves them guessing.
func unauth(w http.ResponseWriter, errCode string) {
	challenge := `Bearer realm="` + serverName + `"`
	if errCode != "" {
		challenge += `, error="` + errCode + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32001, "message": "Unauthorized"},
		"id":      nil,
	})
}
