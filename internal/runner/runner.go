// Package runner executes ddev without a shell in a fixed project directory,
// streaming its output and killing the whole process group on cancel/timeout.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Options controls execution limits.
type Options struct {
	Binary   string        // default "ddev"
	Timeout  time.Duration // default 10 minutes
	MaxBytes int64         // per stream; default 8 MiB
}

// Exit codes produced by the runner itself.
const (
	ExitTimeout   = 124
	ExitCancelled = 130
	ExitNoStart   = 127
)

// capped forwards up to max bytes to w, then drops the rest (but keeps draining).
type capped struct {
	w         io.Writer
	left      int64
	truncated bool
	max       int64
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if c.left <= 0 {
		c.truncated = c.truncated || n > 0
		return n, nil
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
		c.truncated = true
	}
	c.left -= int64(len(p))
	if _, err := c.w.Write(p); err != nil {
		c.left = 0 // receiver is gone; just drain
	}
	return n, nil
}

// lockedWriter serialises writes from the stdout/stderr copy goroutines.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Run executes `<binary> argv...` in dir with empty stdin, streaming output to
// stdout/stderr (writes are serialised). It returns the exit code.
func Run(ctx context.Context, dir string, argv []string, o Options, stdout, stderr io.Writer) int {
	if o.Binary == "" {
		o.Binary = "ddev"
	}
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Minute
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 8 << 20
	}
	tctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	var mu sync.Mutex
	out := &capped{w: lockedWriter{&mu, stdout}, left: o.MaxBytes, max: o.MaxBytes}
	errw := &capped{w: lockedWriter{&mu, stderr}, left: o.MaxBytes, max: o.MaxBytes}

	cmd := exec.CommandContext(tctx, o.Binary, argv...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, errw
	cmd.WaitDelay = 5 * time.Second // after cancel: SIGTERM, then SIGKILL 5s later
	setProcessGroup(cmd)

	err := cmd.Run()
	killProcessGroup(cmd) // reap anything the child left behind

	note := func(format string, a ...any) {
		fmt.Fprintf(lockedWriter{&mu, stderr}, "\n[ddev-bridge: "+format+"]\n", a...)
	}
	for _, c := range []*capped{out, errw} {
		if c.truncated {
			note("output truncated at %d bytes", c.max)
		}
	}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		note("command timed out after %s", o.Timeout)
		return ExitTimeout
	case ctx.Err() != nil:
		return ExitCancelled
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		return 1
	default:
		note("failed to start %s: %v", o.Binary, err)
		return ExitNoStart
	}
}
