// Package tools registers MCP tools on a server.
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onixldlc/mcp-linux-go/internal/executor"
	"github.com/onixldlc/mcp-linux-go/internal/validator"
)

const (
	DefaultTimeoutSec = 20
	MaxTimeoutSec     = 120
	OutputCapBytes    = 1_000_000
	MaxFileRead       = 10_000_000
	MaxFileWrite      = 50_000_000
)

// Register adds all 6 tools to server.
func Register(server *mcp.Server, allow validator.AllowList) {
	registerShellExecute(server, allow)
	registerReadFile(server)
	registerWriteFile(server)
	registerUploadFileB64(server)
	registerListDir(server)
	registerSystemInfo(server, allow)
}

func expandHome(p string) string {
	if p == "~" {
		h, _ := os.UserHomeDir()
		return h
	}
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func ptrBool(b bool) *bool { return &b }

func jsonResult(v any, isError bool) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: v,
		IsError:           isError,
	}
}

// ───────── shell_execute ─────────

type ShellExecuteArgs struct {
	Command   []string `json:"command" jsonschema:"argv tokens, e.g. ['ls','-l','/tmp'] or ['cat','f','|','grep','foo']"`
	Stdin     *string  `json:"stdin,omitempty" jsonschema:"Input piped to first stage"`
	Directory *string  `json:"directory,omitempty" jsonschema:"Working directory (~ expanded)"`
	Timeout   *int     `json:"timeout,omitempty" jsonschema:"Timeout seconds (default 120, max 1800)"`
}

func registerShellExecute(server *mcp.Server, allow validator.AllowList) {
	var allowDesc string
	if allow.Unrestricted {
		allowDesc = "UNRESTRICTED mode — any command allowed. Lab use only."
	} else {
		allowDesc = "Allowlist: " + strings.Join(allow.Sorted(), ", ")
	}
	desc := fmt.Sprintf(`Execute a command as argv (no shell interpretation). Compatible with tumf/mcp-shell-server request format.

%s

Shell operators (`+"`;`, `&&`, `||`, `|`"+`) may appear as separate argv tokens.
They are parsed in-process, not by /bin/sh:
  - `+"`|`"+` chains stdout→stdin between adjacent stages
  - `+"`;`"+` runs next statement unconditionally
  - `+"`&&`"+` runs next only if previous exited 0
  - `+"`||`"+` runs next only if previous exited non-0
Each statement's head command is validated against the allowlist.`, allowDesc)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "shell_execute",
		Title:       "Execute Shell Command",
		Description: desc,
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: ptrBool(true),
			OpenWorldHint:   ptrBool(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ShellExecuteArgs) (*mcp.CallToolResult, any, error) {
		if err := validator.Validate(args.Command, allow); err != nil {
			code := 1
			res := executor.Result{
				Stderr: err.Error(),
				Status: &code,
				Error:  err.Error(),
			}
			return jsonResult(res, true), res, nil
		}

		timeoutSec := DefaultTimeoutSec
		if args.Timeout != nil {
			if *args.Timeout <= 0 || *args.Timeout > MaxTimeoutSec {
				return jsonResult(map[string]any{"error": "invalid timeout"}, true), nil, nil
			}
			timeoutSec = *args.Timeout
		}
		cwd := ""
		if args.Directory != nil {
			cwd = expandHome(*args.Directory)
		}
		opts := executor.Options{
			Cwd:            cwd,
			Timeout:        time.Duration(timeoutSec) * time.Second,
			OutputCapBytes: OutputCapBytes,
		}
		if args.Stdin != nil {
			opts.Stdin = *args.Stdin
			opts.StdinSet = true
		}
		res := executor.Exec(ctx, args.Command, opts)
		return jsonResult(res, false), res, nil
	})
}

// ───────── read_file ─────────

type ReadFileArgs struct {
	Path     string `json:"path" jsonschema:"absolute or ~-relative path"`
	MaxBytes *int   `json:"max_bytes,omitempty" jsonschema:"cap read size (default 10MB)"`
}

type ReadFileOut struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func registerReadFile(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_file",
		Title:       "Read File",
		Description: "Read a file from the host filesystem as UTF-8 text.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: ptrBool(false),
			OpenWorldHint:   ptrBool(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ReadFileArgs) (*mcp.CallToolResult, any, error) {
		full := expandHome(args.Path)
		cap := MaxFileRead
		if args.MaxBytes != nil {
			if *args.MaxBytes <= 0 || *args.MaxBytes > MaxFileRead {
				return nil, nil, fmt.Errorf("max_bytes out of range")
			}
			cap = *args.MaxBytes
		}
		st, err := os.Stat(full)
		if err != nil {
			return nil, nil, err
		}
		f, err := os.Open(full)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		n := st.Size()
		if n > int64(cap) {
			n = int64(cap)
		}
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, 0); err != nil && err.Error() != "EOF" {
			return nil, nil, err
		}
		out := ReadFileOut{
			Path:      full,
			SizeBytes: st.Size(),
			Content:   string(buf),
			Truncated: st.Size() > int64(cap),
		}
		return jsonResult(out, false), out, nil
	})
}

// ───────── write_file ─────────

type WriteFileArgs struct {
	Path         string  `json:"path"`
	Content      string  `json:"content"`
	Mode         *string `json:"mode,omitempty" jsonschema:"octal like '0644'"`
	MkdirParents *bool   `json:"mkdir_parents,omitempty"`
}

type WriteFileOut struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

func registerWriteFile(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_file",
		Title:       "Write File",
		Description: "Write/overwrite a UTF-8 text file.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: ptrBool(true),
			OpenWorldHint:   ptrBool(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args WriteFileArgs) (*mcp.CallToolResult, any, error) {
		if len(args.Content) > MaxFileWrite {
			return nil, nil, fmt.Errorf("content exceeds %d bytes", MaxFileWrite)
		}
		full := expandHome(args.Path)
		if args.MkdirParents != nil && *args.MkdirParents {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return nil, nil, err
			}
		}
		if err := os.WriteFile(full, []byte(args.Content), 0o644); err != nil {
			return nil, nil, err
		}
		if args.Mode != nil {
			m, err := parseMode(*args.Mode)
			if err != nil {
				return nil, nil, err
			}
			if err := os.Chmod(full, m); err != nil {
				return nil, nil, err
			}
		}
		out := WriteFileOut{Path: full, BytesWritten: len(args.Content)}
		return jsonResult(out, false), out, nil
	})
}

func parseMode(s string) (os.FileMode, error) {
	if !isOctalModeString(s) {
		return 0, fmt.Errorf("invalid mode: %s", s)
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(n), nil
}

func isOctalModeString(s string) bool {
	if len(s) < 3 || len(s) > 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '7' {
			return false
		}
	}
	return true
}

// ───────── upload_file_b64 ─────────

type UploadFileB64Args struct {
	Path         string  `json:"path"`
	DataB64      string  `json:"data_b64"`
	Mode         *string `json:"mode,omitempty"`
	MkdirParents *bool   `json:"mkdir_parents,omitempty"`
}

func registerUploadFileB64(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "upload_file_b64",
		Title:       "Upload Binary File (base64)",
		Description: "Write a binary file from base64 data. For installers, archives, certs.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: ptrBool(true),
			OpenWorldHint:   ptrBool(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args UploadFileB64Args) (*mcp.CallToolResult, any, error) {
		full := expandHome(args.Path)
		data, err := base64.StdEncoding.DecodeString(args.DataB64)
		if err != nil {
			return nil, nil, err
		}
		if len(data) > MaxFileWrite {
			return nil, nil, fmt.Errorf("file too large: %d > %d", len(data), MaxFileWrite)
		}
		if args.MkdirParents != nil && *args.MkdirParents {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return nil, nil, err
			}
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return nil, nil, err
		}
		if args.Mode != nil {
			m, err := parseMode(*args.Mode)
			if err != nil {
				return nil, nil, err
			}
			if err := os.Chmod(full, m); err != nil {
				return nil, nil, err
			}
		}
		out := WriteFileOut{Path: full, BytesWritten: len(data)}
		return jsonResult(out, false), out, nil
	})
}

// ───────── list_dir ─────────

type ListDirArgs struct {
	Path       string `json:"path"`
	ShowHidden *bool  `json:"show_hidden,omitempty"`
}

type ListDirEntry struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

type ListDirOut struct {
	Path    string         `json:"path"`
	Count   int            `json:"count"`
	Entries []ListDirEntry `json:"entries"`
}

func registerListDir(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_dir",
		Title:       "List Directory",
		Description: "List directory entries with type + size.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: ptrBool(false),
			OpenWorldHint:   ptrBool(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListDirArgs) (*mcp.CallToolResult, any, error) {
		full := expandHome(args.Path)
		entries, err := os.ReadDir(full)
		if err != nil {
			return nil, nil, err
		}
		showHidden := args.ShowHidden != nil && *args.ShowHidden
		out := ListDirOut{Path: full, Entries: []ListDirEntry{}}
		for _, e := range entries {
			name := e.Name()
			if !showHidden && strings.HasPrefix(name, ".") {
				continue
			}
			st, err := os.Lstat(filepath.Join(full, name))
			if err != nil {
				out.Entries = append(out.Entries, ListDirEntry{Name: name, Type: "unreadable"})
				continue
			}
			t := "other"
			switch {
			case st.Mode()&os.ModeSymlink != 0:
				t = "symlink"
			case st.IsDir():
				t = "dir"
			case st.Mode().IsRegular():
				t = "file"
			}
			entry := ListDirEntry{Name: name, Type: t}
			if t == "file" {
				sz := st.Size()
				entry.SizeBytes = &sz
			}
			out.Entries = append(out.Entries, entry)
		}
		out.Count = len(out.Entries)
		sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
		return jsonResult(out, false), out, nil
	})
}

// ───────── system_info ─────────

type SystemInfoArgs struct{}

func registerSystemInfo(server *mcp.Server, allow validator.AllowList) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "system_info",
		Title:       "System Info",
		Description: "Return basic host info: hostname, platform, arch, user, cwd, uptime, memory.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: ptrBool(false),
			OpenWorldHint:   ptrBool(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ SystemInfoArgs) (*mcp.CallToolResult, any, error) {
		host, _ := os.Hostname()
		u, _ := user.Current()
		cwd, _ := os.Getwd()
		home, _ := os.UserHomeDir()
		info := map[string]any{
			"hostname":     host,
			"platform":     runtime.GOOS,
			"arch":         runtime.GOARCH,
			"go_version":   runtime.Version(),
			"user":         userName(u),
			"home":         home,
			"cwd":          cwd,
			"uptime_sec":   uptimeSec(),
			"total_mem_mb": totalMemMB(),
			"allow_mode":   map[bool]string{true: "unrestricted", false: "allowlist"}[allow.Unrestricted],
		}
		if !allow.Unrestricted {
			info["allowed_commands"] = allow.Sorted()
		} else {
			info["allowed_commands"] = nil
		}
		return jsonResult(info, false), info, nil
	})
}

func userName(u *user.User) string {
	if u == nil {
		return ""
	}
	return u.Username
}
