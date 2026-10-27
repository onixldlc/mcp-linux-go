package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onixldlc/mcp-linux-go/internal/tools"
	"github.com/onixldlc/mcp-linux-go/internal/validator"
)

const serverName = "mcp-linux-go"

// serverVersion overridden at build time via:
//
//	go build -ldflags "-X main.serverVersion=$(git describe --tags --always --dirty)"
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
	host           string
	port           int
	token          string
	tokenGenerated bool
	allowCommands  string
	trustedHosts   string
	logRequests    bool
}

// logf writes a timestamped server line to stderr. Rejections always log; full
// per-request lines only under --log-requests.
func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[%s %s] %s\n",
		serverName, time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

// reqDesc summarizes a request for the log without leaking bodies or tokens.
// Mcp-Method/Mcp-Name are the SEP-2243 headers real clients mirror the JSON-RPC
// call into, so they identify the call without parsing the body.
func reqDesc(r *http.Request) string {
	desc := fmt.Sprintf("%s %s host=%q from=%s", r.Method, r.URL.Path, r.Host, r.RemoteAddr)
	if m := r.Header.Get("Mcp-Method"); m != "" {
		desc += " mcp-method=" + m
	}
	if n := r.Header.Get("Mcp-Name"); n != "" {
		desc += " mcp-name=" + n
	}
	if v := r.Header.Get("Mcp-Protocol-Version"); v != "" {
		desc += " proto=" + v
	}
	return desc
}

// statusRecorder captures the response status for the access log. It forwards
// Flush because the streamable HTTP transport streams SSE responses.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestLog is the outermost middleware so it observes the status actually
// sent, including 401/403 produced by the guards below it.
func requestLog(enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		logf("%d %s ua=%q %s", rec.status, reqDesc(r),
			r.Header.Get("User-Agent"), time.Since(start).Round(time.Millisecond))
	})
}

// parseTrustedHosts builds the set of extra Host header values accepted by
// hostGuard, keyed by hostname with any port stripped.
func parseTrustedHosts(csv string) map[string]bool {
	set := map[string]bool{}
	for _, h := range strings.Split(csv, ",") {
		if h = strings.TrimSpace(h); h != "" {
			set[strings.ToLower(hostnameOnly(h))] = true
		}
	}
	return set
}

func hostnameOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func isLoopbackHost(hostport string) bool {
	h := hostnameOnly(hostport)
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// hostGuard reimplements the SDK's DNS-rebinding check with an allowlist.
//
// The SDK rejects any request whose Host header is non-loopback when the
// connection landed on a loopback local address. That breaks container clients:
// Docker/Podman forward "host.docker.internal" into the host's loopback, so the
// connection is loopback but the Host header is not, and the request 403s.
//
// Naming a host here is far weaker than disabling the check outright. Rebinding
// requires the attacker to control a DNS name resolving to 127.0.0.1 and to make
// the victim send it as Host; that only works if the attacker controls the name
// being trusted. Names like "host.docker.internal" are container-runtime-local
// and not registrable by an attacker.
func hostGuard(trusted map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if ok && localAddr != nil && isLoopbackHost(localAddr.String()) &&
			!isLoopbackHost(r.Host) && !trusted[strings.ToLower(hostnameOnly(r.Host))] {
			logf("403 DNS-rebinding guard rejected %s", reqDesc(r))
			logf("    if that name is yours, restart with --trusted-hosts %q (or TRUSTED_HOSTS=%s)",
				hostnameOnly(r.Host), hostnameOnly(r.Host))
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, "Forbidden: rejected Host header %q (DNS-rebinding protection).\n"+
				"The server is on loopback but the client addressed it by another name.\n"+
				"If that name is legitimate, allow it explicitly:\n"+
				"  --trusted-hosts %q     (or TRUSTED_HOSTS=%s)\n",
				r.Host, hostnameOnly(r.Host), hostnameOnly(r.Host))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func printHelp() {
	fmt.Printf(`%s v%s

Usage: %s [options]

Options:
  -H, --host <addr>              HTTP bind address (default: 127.0.0.1) [env: HOST]
  -p, --port <num>               HTTP port (default: 3000)              [env: PORT]
      --token <str>              Bearer token for HTTP (>=16 chars). "auto"
                                 mints one per run and prints it in the banner.
                                                                        [env: SHELL_MCP_TOKEN]
      --allow-commands <csv>     Comma-sep allowlist. Unset = unrestricted.
                                                                        [env: ALLOW_COMMANDS]
      --trusted-hosts <csv>      Extra Host header values to accept when the
                                 server listens on loopback.
                                 e.g. host.docker.internal             [env: TRUSTED_HOSTS]
      --log-requests             Log every HTTP request (method, path, Host,
                                 MCP method, status). Rejections always log.
                                                                       [env: LOG_REQUESTS]
  -h, --help                     Show this help
  -v, --version                  Show version

CLI flags override env vars. Env vars override defaults.

Examples:
  %s --allow-commands "ls,cat,pwd,grep"
  %s -H 127.0.0.1 -p 3000 --token "$(openssl rand -hex 32)"
  %s --token auto                             # server mints and prints one
  %s --trusted-hosts "host.docker.internal"   # client runs in a container

Generate an HTTP token:
  openssl rand -hex 32
`, serverName, serverVersion, serverName, serverName, serverName, serverName, serverName)
}

func parseConfig() config {
	fs := flag.NewFlagSet(serverName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		host        string
		hostShort   string
		port        int
		portShort   int
		token       string
		allow       string
		trusted     string
		logReqs     bool
		help, helpS bool
		ver, verS   bool
	)
	fs.StringVar(&host, "host", "", "HTTP bind address")
	fs.StringVar(&hostShort, "H", "", "HTTP bind address (short)")
	fs.IntVar(&port, "port", 0, "HTTP port")
	fs.IntVar(&portShort, "p", 0, "HTTP port (short)")
	fs.StringVar(&token, "token", "", "Bearer token, or \"auto\" to mint one")
	fs.StringVar(&allow, "allow-commands", "", "Comma-sep allowlist")
	fs.StringVar(&trusted, "trusted-hosts", "", "Comma-sep extra Host header values")
	fs.BoolVar(&logReqs, "log-requests", false, "Log every HTTP request")
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
	tokenGenerated := false
	if strings.EqualFold(tokenVal, "auto") {
		tokenVal = generateToken()
		tokenGenerated = true
	}
	trustedVal := trusted
	if trustedVal == "" {
		trustedVal = os.Getenv("TRUSTED_HOSTS")
	}
	logReqsVal := logReqs
	if !logReqsVal {
		switch strings.ToLower(os.Getenv("LOG_REQUESTS")) {
		case "1", "true", "yes":
			logReqsVal = true
		}
	}

	return config{
		host: hostVal, port: portRaw, token: tokenVal,
		tokenGenerated: tokenGenerated,
		allowCommands:  allowVal, trustedHosts: trustedVal,
		logRequests: logReqsVal,
	}
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

// generateToken returns 32 random bytes hex encoded, as "openssl rand -hex 32".
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] FATAL: cannot generate a token: %v\n", serverName, err)
		os.Exit(1)
	}
	return hex.EncodeToString(b)
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

	if t := parseTrustedHosts(cfg.trustedHosts); len(t) > 0 {
		names := make([]string, 0, len(t))
		for h := range t {
			names = append(names, h)
		}
		sort.Strings(names)
		log("trusted-hosts: " + strings.Join(names, ", ") + " (plus loopback)")
	}

	if cfg.logRequests {
		log("logging:     per-request (--log-requests)")
	} else {
		log("logging:     rejections only — add --log-requests for every request")
	}

	log(fmt.Sprintf("bind:        http://%s:%d/mcp", cfg.host, cfg.port))
	log(fmt.Sprintf("health:      http://%s:%d/healthz", cfg.host, cfg.port))
	if cfg.token != "" && len(cfg.token) >= 16 {
		if cfg.tokenGenerated {
			log("auth:        token generated for this run (--token auto) — it lives nowhere else:")
		}
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
		// hostGuard below performs this check with an allowlist the SDK's
		// blanket version does not support.
		DisableLocalhostProtection: true,
	})

	// No http.CrossOriginProtection wrapper here, deliberately. It rejects any
	// request carrying "Origin: null" or "Sec-Fetch-Site: cross-site" with a 403,
	// which breaks legitimate non-browser MCP clients, and it buys nothing for
	// this server:
	//   - Browser CSRF cannot set "Authorization: Bearer <secret>" cross-origin;
	//     a custom header forces a CORS preflight this server never approves.
	//     Auth below already blocks that entire class.
	//   - DNS rebinding is handled by the SDK's own localhost protection
	//     (StreamableHTTPOptions.DisableLocalhostProtection, on by default),
	//     which rejects a non-localhost Host header on a localhost connection.
	// Bearer auth is outermost so unauthenticated calls get a 401 challenge
	// rather than an opaque 403.
	trusted := parseTrustedHosts(cfg.trustedHosts)
	protect := func(h http.Handler) http.Handler {
		return requestLog(cfg.logRequests, hostGuard(trusted, bearerAuth(cfg.token, h)))
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
			reason := "missing Authorization header"
			if h != "" {
				reason = "malformed Authorization header (expected \"Bearer <token>\")"
			}
			logf("401 %s — %s", reqDesc(r), reason)
			unauth(w, "")
			return
		}
		got := []byte(h[len(prefix):])
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			// Fingerprints only — never log the presented token itself.
			logf("401 %s — token mismatch (presented fp %s, expected fp %s)",
				reqDesc(r), tokenFingerprint(string(got)), tokenFingerprint(token))
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
