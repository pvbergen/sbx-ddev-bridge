package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func okProbe(string) (string, error) { return "ok v1", nil }

func decodeContext(t *testing.T, b []byte) string {
	t.Helper()
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("invalid hook output %q: %v", b, err)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q", out.HookSpecificOutput.HookEventName)
	}
	return out.HookSpecificOutput.AdditionalContext
}

func TestSessionStartOnHostIsSilent(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.sh")
	var buf bytes.Buffer
	err := sessionStart(sessionEnv{envFile: envFile, binDir: "/p/.ddev-bridge/bin", probe: okProbe}, &buf)
	if err != nil || buf.Len() != 0 {
		t.Fatalf("host: output %q err %v", buf.String(), err)
	}
	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Fatal("host: env file must not be touched")
	}
}

func TestSessionStartInSandbox(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.sh")
	os.WriteFile(envFile, []byte("export FOO=1"), 0o644) // no trailing newline
	e := sessionEnv{sandbox: "sb", envFile: envFile, binDir: "/p q/.ddev-bridge/bin", base: "http://h:7474", probe: okProbe}
	for i := 0; i < 2; i++ {
		var buf bytes.Buffer
		if err := sessionStart(e, &buf); err != nil {
			t.Fatal(err)
		}
		ctx := decodeContext(t, buf.Bytes())
		if !strings.Contains(ctx, "# DDEV Bridge: Agent Guide") || !strings.Contains(ctx, "reachable at http://h:7474 (ok v1)") {
			t.Fatalf("context: %s", ctx)
		}
	}
	b, _ := os.ReadFile(envFile)
	if want := "export FOO=1\n" + pathLine("/p q/.ddev-bridge/bin") + "\n"; string(b) != want {
		t.Fatalf("env file = %q, want %q", b, want)
	}

	// Sourcing the line repeatedly prepends the directory exactly once.
	script := ". " + shellQuote(envFile) + "; . " + shellQuote(envFile) + `; printf %s "$PATH"`
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	if p := string(out); !strings.HasPrefix(p, "/p q/.ddev-bridge/bin:") || strings.Count(p, "/p q/.ddev-bridge/bin") != 1 {
		t.Fatalf("PATH = %q", p)
	}
}

func TestSessionStartReportsProblems(t *testing.T) {
	failProbe := func(string) (string, error) { return "", errors.New("connection refused") }

	var buf bytes.Buffer
	sessionStart(sessionEnv{sandbox: "sb", binDir: "/b", base: "http://h:7474", probe: failProbe}, &buf)
	ctx := decodeContext(t, buf.Bytes())
	if !strings.Contains(ctx, "CLAUDE_ENV_FILE` is not set") || !strings.Contains(ctx, "not reachable at http://h:7474 (connection refused)") {
		t.Fatalf("context: %s", ctx)
	}

	buf.Reset()
	probed := false
	clone := sessionEnv{sandbox: "sb", envFile: filepath.Join(t.TempDir(), "env.sh"), binDir: "/b", clone: true,
		probe: func(string) (string, error) { probed = true; return "", nil }}
	if err := sessionStart(clone, &buf); err != nil {
		t.Fatal(err)
	}
	if ctx := decodeContext(t, buf.Bytes()); !strings.Contains(ctx, "clone mode") || probed {
		t.Fatalf("clone mode: probed=%v context: %s", probed, ctx)
	}
}
