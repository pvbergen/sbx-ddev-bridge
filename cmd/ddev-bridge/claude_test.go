package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

func countHooks(m map[string]any) int {
	n := 0
	hooks, _ := m["hooks"].(map[string]any)
	groups, _ := hooks["SessionStart"].([]any)
	for _, g := range groups {
		hs, _ := g.(map[string]any)["hooks"].([]any)
		for _, h := range hs {
			if isOurHook(h) {
				n++
			}
		}
	}
	return n
}

func TestClaudeSettingsMissingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, claudeSettings)
	if changed, err := mergeClaudeSettings(atRoot(t, path)); err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}
	m := readJSON(t, path)
	ask := m["permissions"].(map[string]any)["ask"].([]any)
	if countHooks(m) != 1 || len(ask) != len(claudeAskRules) {
		t.Fatalf("settings: %v", m)
	}
	if want := `"$CLAUDE_PROJECT_DIR"/.ddev-bridge/bin/ddev-bridge-client --session-start`; claudeHookCommand != want {
		t.Fatalf("hook command = %q, want %q", claudeHookCommand, want)
	}

	// Removal deletes the file and the then empty .claude/.
	if changed, err := removeClaudeSettings(atRoot(t, path)); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal(".claude/ should be gone")
	}
	if changed, err := removeClaudeSettings(atRoot(t, path)); err != nil || changed {
		t.Fatalf("remove without a file: changed=%v err=%v", changed, err)
	}
}

func TestClaudeSettingsKeepsExistingEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.local.json")
	orig := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls:*)"], "ask": ["Bash(git push:*)", "Bash(drush cim:*)"]},
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "true"}]}]
  }
}`
	os.WriteFile(path, []byte(orig), 0o644)

	for i := 0; i < 2; i++ {
		changed, err := mergeClaudeSettings(atRoot(t, path))
		if err != nil || changed != (i == 0) {
			t.Fatalf("merge %d: changed=%v err=%v", i, changed, err)
		}
	}
	m := readJSON(t, path)
	ask := m["permissions"].(map[string]any)["ask"].([]any)
	if countHooks(m) != 1 || len(ask) != len(claudeAskRules)+1 || m["model"] != "opus" {
		t.Fatalf("after merge: %v", m)
	}
	if strings.Count(mustRead(t, path), `"Bash(drush cim:*)"`) != 1 {
		t.Fatal("existing ask rule duplicated")
	}

	// Removal keeps everything that isn't ours, including a rule we also add.
	if changed, err := removeClaudeSettings(atRoot(t, path)); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	var want map[string]any
	json.Unmarshal([]byte(orig), &want)
	want["permissions"].(map[string]any)["ask"] = []any{"Bash(git push:*)"}
	if got := readJSON(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("after remove:\n got %v\nwant %v", got, want)
	}
}

func TestClaudeSettingsInvalidLeftUntouched(t *testing.T) {
	for _, content := range []string{`{"model": `, `[]`, `{"hooks": []}`, `{"permissions": {"ask": "x"}}`} {
		path := filepath.Join(t.TempDir(), "settings.local.json")
		os.WriteFile(path, []byte(content), 0o644)
		if _, err := mergeClaudeSettings(atRoot(t, path)); !errors.Is(err, errClaudeSettings) {
			t.Fatalf("merge %q: err=%v", content, err)
		}
		if _, err := removeClaudeSettings(atRoot(t, path)); !errors.Is(err, errClaudeSettings) {
			t.Fatalf("remove %q: err=%v", content, err)
		}
		if got := mustRead(t, path); got != content {
			t.Fatalf("file changed: %q", got)
		}
	}
	if !strings.Contains(claudeSnippet(), `"SessionStart"`) || !strings.Contains(claudeSnippet(), `"ask"`) {
		t.Fatalf("snippet: %s", claudeSnippet())
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
