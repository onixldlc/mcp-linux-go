// Package executor runs a validated argv pipeline.
//
// We never invoke /bin/sh. Each stage is exec'd directly.
// Operator semantics (emulated in-process):
//
//	a | b    -> pipe a.stdout to b.stdin
//	a ; b    -> run a, then b (regardless of exit)
//	a && b   -> run a, then b only if a.exit == 0
//	a || b   -> run a, then b only if a.exit != 0
//
// Consecutive `|` segments form a pipeline.
// `;`/`&&`/`||` are statement breaks.
package executor

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Options configures a pipeline run.
type Options struct {
	Cwd            string
	Timeout        time.Duration
	Stdin          string
	StdinSet       bool // true if Stdin was explicitly provided
	OutputCapBytes int
}

// Result is the aggregate result of an argv run.
type Result struct {
	Stdout        string  `json:"stdout"`
	Stderr        string  `json:"stderr"`
	Status        *int    `json:"status"`
	ExecutionTime float64 `json:"execution_time"`
	TimedOut      bool    `json:"timed_out"`
	Truncated     bool    `json:"truncated"`
	Error         string  `json:"error,omitempty"`
}

type connector int

const (
	connNone connector = iota
	connSemi
	connAnd
	connOr
)

type statement struct {
	pipeline  [][]string // each entry is argv for one stage
	connector connector  // applied AFTER this statement
}

func parseStatements(argv []string) []statement {
	var stmts []statement
	var pipe [][]string
	var seg []string

	flushSeg := func() {
		if len(seg) > 0 {
			pipe = append(pipe, seg)
			seg = nil
		}
	}
	flushStmt := func(c connector) {
		flushSeg()
		if len(pipe) > 0 {
			stmts = append(stmts, statement{pipeline: pipe, connector: c})
			pipe = nil
		}
	}

	for _, tok := range argv {
		switch tok {
		case "|":
			flushSeg()
		case ";":
			flushStmt(connSemi)
		case "&&":
			flushStmt(connAnd)
		case "||":
			flushStmt(connOr)
		default:
			seg = append(seg, tok)
		}
	}
	flushStmt(connNone)
	return stmts
}

type pipelineOut struct {
	stdout    []byte
	stderr    []byte
	exitCode  *int
	truncated bool
	timedOut  bool
}

// runPipeline runs a single pipeline (one or more stages joined by `|`).
func runPipeline(ctx context.Context, pipe [][]string, stdinStr string, stdinSet bool, cwd string, cap int) pipelineOut {
	n := len(pipe)
	cmds := make([]*exec.Cmd, n)

	var stderrBufs = make([]bytes.Buffer, n)
	var stdoutBuf cappedBuf
	stdoutBuf.cap = cap

	for i, argv := range pipe {
		c := exec.CommandContext(ctx, argv[0], argv[1:]...)
		if cwd != "" {
			c.Dir = cwd
		}
		c.Stderr = &stderrBufs[i]
		cmds[i] = c
	}

	// Wire pipes: cmds[i].Stdout -> cmds[i+1].Stdin
	for i := 0; i < n-1; i++ {
		pr, pw := io.Pipe()
		cmds[i].Stdout = pw
		cmds[i+1].Stdin = pr
	}
	cmds[n-1].Stdout = &stdoutBuf

	// First stage stdin
	var firstStdinWriter io.WriteCloser
	if stdinSet {
		pr, pw := io.Pipe()
		cmds[0].Stdin = pr
		firstStdinWriter = pw
	}

	// Start all
	spawnErrs := make([]error, n)
	started := make([]bool, n)
	for i, c := range cmds {
		if err := c.Start(); err != nil {
			spawnErrs[i] = err
			continue
		}
		started[i] = true
	}

	// Feed stdin (after start)
	if firstStdinWriter != nil {
		go func() {
			_, _ = io.WriteString(firstStdinWriter, stdinStr)
			_ = firstStdinWriter.Close()
		}()
	}

	// Wait. After each wait, close its stdout writer so downstream reader hits EOF.
	var wg sync.WaitGroup
	waitErrs := make([]error, n)
	for i := 0; i < n; i++ {
		if !started[i] {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			waitErrs[idx] = cmds[idx].Wait()
			// Close the pipe writer feeding next stage so it sees EOF.
			if idx < n-1 {
				if pw, ok := cmds[idx].Stdout.(*io.PipeWriter); ok {
					_ = pw.Close()
				}
			}
		}(i)
	}
	wg.Wait()

	var exitCode *int
	last := cmds[n-1]
	if started[n-1] {
		code := last.ProcessState.ExitCode()
		exitCode = &code
	}

	// Aggregate stderr
	var stderrAll bytes.Buffer
	for i := range stderrBufs {
		stderrAll.Write(stderrBufs[i].Bytes())
	}
	for i, e := range spawnErrs {
		if e != nil {
			stderrAll.WriteString("\n[stage ")
			stderrAll.WriteString(itoa(i))
			stderrAll.WriteString(" spawn error] ")
			stderrAll.WriteString(e.Error())
		}
	}

	timedOut := ctx.Err() == context.DeadlineExceeded

	return pipelineOut{
		stdout:    stdoutBuf.Bytes(),
		stderr:    stderrAll.Bytes(),
		exitCode:  exitCode,
		truncated: stdoutBuf.truncated,
		timedOut:  timedOut,
	}
}

// Exec runs argv end-to-end honoring ; && || | connectors.
func Exec(parent context.Context, argv []string, opts Options) Result {
	start := time.Now()
	stmts := parseStatements(argv)
	if len(stmts) == 0 {
		code := 1
		return Result{Status: &code, Error: "Empty command"}
	}

	var aggOut, aggErr cappedBuf
	aggOut.cap = opts.OutputCapBytes
	aggErr.cap = opts.OutputCapBytes
	var lastExit *int
	var timedOut bool
	skipNext := false

	stdinStr := opts.Stdin
	stdinSet := opts.StdinSet

	deadline := start.Add(opts.Timeout)

	for _, s := range stmts {
		if skipNext {
			skipNext = decideSkip(s.connector, lastExit)
			continue
		}

		remaining := time.Until(deadline)
		if remaining < 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(parent, remaining)
		pr := runPipeline(ctx, s.pipeline, stdinStr, stdinSet, opts.Cwd, opts.OutputCapBytes)
		cancel()

		stdinSet = false
		stdinStr = ""

		aggOut.Write(pr.stdout)
		aggErr.Write(pr.stderr)
		if pr.truncated {
			aggOut.truncated = true
		}
		if pr.timedOut {
			timedOut = true
		}
		lastExit = pr.exitCode

		if timedOut {
			break
		}
		skipNext = decideSkip(s.connector, lastExit)
	}

	return Result{
		Stdout:        string(aggOut.Bytes()),
		Stderr:        string(aggErr.Bytes()),
		Status:        lastExit,
		ExecutionTime: time.Since(start).Seconds(),
		TimedOut:      timedOut,
		Truncated:     aggOut.truncated || aggErr.truncated,
	}
}

func decideSkip(c connector, lastExit *int) bool {
	switch c {
	case connNone, connSemi:
		return false
	case connAnd:
		return lastExit == nil || *lastExit != 0
	case connOr:
		return lastExit != nil && *lastExit == 0
	}
	return false
}

// cappedBuf is an io.Writer with a byte cap. Writes past cap are dropped, truncated=true.
type cappedBuf struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (c *cappedBuf) Write(p []byte) (int, error) {
	if c.cap <= 0 {
		return c.buf.Write(p)
	}
	room := c.cap - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.truncated = true
		c.buf.Write(p[:room])
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuf) Bytes() []byte { return c.buf.Bytes() }

// small itoa avoiding strconv import churn
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	bp := len(b)
	for i > 0 {
		bp--
		b[bp] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		bp--
		b[bp] = '-'
	}
	return string(b[bp:])
}

