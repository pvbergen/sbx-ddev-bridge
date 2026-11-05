package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed agent-guide.md
var agentGuide string

// sessionEnv is what --session-start looks at; tests fill it directly.
type sessionEnv struct {
	sandbox string // $SANDBOX_NAME; empty on the host
	envFile string // $CLAUDE_ENV_FILE
	binDir  string // absolute .ddev-bridge/bin
	base    string // bridge URL
	clone   bool
	probe   func(base string) (string, error)
}

// sessionStart implements the Claude Code SessionStart hook. On the host it
// prints nothing. In a sandbox it puts binDir on PATH via $CLAUDE_ENV_FILE and
// prints the agent guide plus a bridge status line as additionalContext. It
// never fails: problems are reported to the agent in the context instead.
func sessionStart(e sessionEnv, w io.Writer) error {
	if e.sandbox == "" {
		return nil
	}
	var notes []string
	if e.envFile == "" {
		notes = append(notes, fmt.Sprintf("`$CLAUDE_ENV_FILE` is not set; put the bridge on PATH yourself: export PATH=%s:\"$PATH\"", shellQuote(e.binDir)))
	} else if err := ensureLine(e.envFile, pathLine(e.binDir)); err != nil {
		notes = append(notes, fmt.Sprintf("could not add %s to PATH via %s (%v); call it by its full path", e.binDir, e.envFile, err))
	} else {
		notes = append(notes, fmt.Sprintf("`ddev` and `drush` in shell commands run through the bridge client in %s.", e.binDir))
	}
	switch {
	case e.clone:
		notes = append(notes, "This sandbox runs in clone mode, so the bridge client refuses to run (host ddev would not see your edits). Do not use ddev or drush; tell the user a direct-mode sandbox is needed.")
	default:
		if v, err := e.probe(e.base); err != nil {
			notes = append(notes, fmt.Sprintf("The bridge is not reachable at %s (%v). Ask the user to run `ddev start` on the host, which starts the bridge, and to allow `localhost:7474` in the sbx network policy.", e.base, err))
		} else {
			notes = append(notes, fmt.Sprintf("The bridge is reachable at %s (%s).", e.base, v))
		}
	}
	ctx := strings.TrimSpace(agentGuide) + "\n\n## Status\n- " + strings.Join(notes, "\n- ") + "\n"
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": ctx,
	}}
	return json.NewEncoder(w).Encode(out)
}

// pathLine prepends dir to PATH once. In sbx, $CLAUDE_ENV_FILE is sourced
// before every command, so the line must not grow PATH on each run.
func pathLine(dir string) string {
	q := shellQuote(dir)
	return fmt.Sprintf(`case ":$PATH:" in *:%s:*) ;; *) export PATH=%s:"$PATH" ;; esac # ddev-bridge`, q, q)
}

// ensureLine appends line to path unless the file already has it.
func ensureLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if l == line {
			return nil
		}
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%s%s\n", prefix, line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// probeBridge asks the bridge's health endpoint for its version.
func probeBridge(base string) (string, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(strings.TrimRight(base, "/") + "/healthz")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("health check returned %s", resp.Status)
	}
	return strings.TrimSpace(string(b)), nil
}

// binDir is the directory of the running client (.ddev-bridge/bin).
func binDir() string {
	exe, err := os.Executable()
	if err != nil {
		exe, _ = filepath.Abs(os.Args[0])
	}
	return filepath.Dir(exe)
}
