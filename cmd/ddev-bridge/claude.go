package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"ddev-bridge/internal/registry"
)

// claudeSettings is the per-developer Claude Code settings file `enable`
// extends with its sandbox overlay. Only the entries below are added or
// removed; everything else in the file is kept.
var claudeSettings = filepath.Join(".claude", "settings.local.json")

var claudeClient = `"$CLAUDE_PROJECT_DIR"/` + filepath.ToSlash(filepath.Join(registry.Dir, "bin", "ddev-bridge-client"))

// claudeHook is one hook entry `enable` adds, under event (and matcher).
type claudeHook struct{ event, matcher, command string }

// claudeHooks: SessionStart does nothing on the host, so it is harmless
// outside a sandbox. PreToolUse asks before SQL that is not a plain read, on
// the host and in the sandbox alike.
var claudeHooks = []claudeHook{
	{"SessionStart", "", claudeClient + " --session-start"},
	{"PreToolUse", "Bash", claudeClient + " --pre-tool-use"},
}

// claudeAskRules make Claude Code confirm commands that change data, config,
// dependencies or the environment. The bridge allows them; this is a prompt,
// not a boundary. SQL queries are left to the PreToolUse hook.
var claudeAskRules = func() []string {
	var rules []string
	for _, c := range []string{"cim", "config:import", "config:set", "pmu", "pm:uninstall", "si", "site:install", "sql:drop", "deploy"} {
		rules = append(rules, "Bash(drush "+c+":*)", "Bash(ddev drush "+c+":*)")
	}
	for _, c := range []string{"composer require", "composer update", "composer remove", "import-db", "snapshot restore", "stop", "restart"} {
		rules = append(rules, "Bash(ddev "+c+":*)")
	}
	return rules
}()

// claudeOldAskRules were added by earlier versions; enable and uninstall
// remove them. sql:query prompted for every read.
var claudeOldAskRules = []string{"Bash(drush sql:query:*)", "Bash(ddev drush sql:query:*)"}

// errClaudeSettings marks a settings file we leave alone because it is not
// valid JSON or has an unexpected shape.
var errClaudeSettings = errors.New("not a valid Claude Code settings file")

func (h claudeHook) group() map[string]any {
	g := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": h.command}}}
	if h.matcher != "" {
		g["matcher"] = h.matcher
	}
	return g
}

// claudeSnippet is what to merge by hand when the file can't be edited.
func claudeSnippet() string {
	hooks := map[string]any{}
	for _, h := range claudeHooks {
		hooks[h.event] = []any{h.group()}
	}
	b, _ := json.MarshalIndent(map[string]any{
		"hooks":       hooks,
		"permissions": map[string]any{"ask": claudeAskRules},
	}, "", "  ")
	return string(b)
}

// loadSettings reads path into a map; a missing file is an empty map.
func loadSettings(r *os.Root, path string) (map[string]any, bool, error) {
	data, err := r.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	} else if err != nil {
		return nil, false, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, true, errClaudeSettings
	}
	return m, true, nil
}

// object returns m[key] as an object, creating it if create is set.
func object(m map[string]any, key string, create bool) (map[string]any, error) {
	v, ok := m[key]
	if !ok {
		if !create {
			return nil, nil
		}
		o := map[string]any{}
		m[key] = o
		return o, nil
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, errClaudeSettings
	}
	return o, nil
}

func array(m map[string]any, key string) ([]any, error) {
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, errClaudeSettings
	}
	return a, nil
}

// isHook reports whether entry is the hook entry running command.
func isHook(entry any, command string) bool {
	o, ok := entry.(map[string]any)
	return ok && o["command"] == command
}

// addHook adds h under hooks[h.event] unless an entry runs its command.
func addHook(hooks map[string]any, h claudeHook) (bool, error) {
	groups, err := array(hooks, h.event)
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		grp, ok := g.(map[string]any)
		if !ok {
			return false, errClaudeSettings
		}
		hs, err := array(grp, "hooks")
		if err != nil {
			return false, err
		}
		for _, e := range hs {
			if isHook(e, h.command) {
				return false, nil
			}
		}
	}
	hooks[h.event] = append(groups, h.group())
	return true, nil
}

// removeHook removes the entries running h.command from hooks[h.event] and
// drops groups and events it leaves empty.
func removeHook(hooks map[string]any, h claudeHook) (bool, error) {
	groups, err := array(hooks, h.event)
	if err != nil {
		return false, err
	}
	removed := false
	var keepGroups []any
	for _, g := range groups {
		grp, ok := g.(map[string]any)
		if !ok {
			return false, errClaudeSettings
		}
		hs, err := array(grp, "hooks")
		if err != nil {
			return false, err
		}
		var keep []any
		for _, e := range hs {
			if isHook(e, h.command) {
				removed = true
			} else {
				keep = append(keep, e)
			}
		}
		if len(keep) == len(hs) {
			keepGroups = append(keepGroups, g)
		} else if len(keep) > 0 {
			grp["hooks"] = keep
			keepGroups = append(keepGroups, g)
		}
	}
	if removed {
		if len(keepGroups) > 0 {
			hooks[h.event] = keepGroups
		} else {
			delete(hooks, h.event)
		}
	}
	return removed, nil
}

// withoutRules returns rules minus drop.
func withoutRules(rules []any, drop []string) []any {
	d := map[any]bool{}
	for _, r := range drop {
		d[r] = true
	}
	var keep []any
	for _, r := range rules {
		if !d[r] {
			keep = append(keep, r)
		}
	}
	return keep
}

func writeSettings(r *os.Root, path string, m map[string]any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	if err := r.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return r.WriteFile(path, buf.Bytes(), 0o644)
}

// mergeClaudeSettings adds the hooks and the ask rules that are
// missing. It writes only if something changed.
func mergeClaudeSettings(r *os.Root, path string) (bool, error) {
	m, _, err := loadSettings(r, path)
	if err != nil {
		return false, err
	}
	changed := false

	hooks, err := object(m, "hooks", true)
	if err != nil {
		return false, err
	}
	for _, h := range claudeHooks {
		added, err := addHook(hooks, h)
		if err != nil {
			return false, err
		}
		changed = changed || added
	}

	perms, err := object(m, "permissions", true)
	if err != nil {
		return false, err
	}
	ask, err := array(perms, "ask")
	if err != nil {
		return false, err
	}
	if keep := withoutRules(ask, claudeOldAskRules); len(keep) < len(ask) {
		ask, changed = keep, true
	}
	have := map[any]bool{}
	for _, r := range ask {
		have[r] = true
	}
	for _, r := range claudeAskRules {
		if !have[r] {
			ask = append(ask, r)
			changed = true
		}
	}
	perms["ask"] = ask

	if !changed {
		return false, nil
	}
	return true, writeSettings(r, path, m)
}

// removeClaudeSettings removes exactly what mergeClaudeSettings adds, drops
// containers it leaves empty, and deletes the file (and an empty .claude/) if
// nothing else is left.
func removeClaudeSettings(r *os.Root, path string) (bool, error) {
	m, exists, err := loadSettings(r, path)
	if err != nil || !exists {
		return false, err
	}
	changed := false

	hooks, err := object(m, "hooks", false)
	if err != nil {
		return false, err
	}
	if hooks != nil {
		for _, h := range claudeHooks {
			removed, err := removeHook(hooks, h)
			if err != nil {
				return false, err
			}
			if removed && len(hooks) == 0 {
				delete(m, "hooks")
			}
			changed = changed || removed
		}
	}

	perms, err := object(m, "permissions", false)
	if err != nil {
		return false, err
	}
	if perms != nil {
		ask, err := array(perms, "ask")
		if err != nil {
			return false, err
		}
		keep := withoutRules(ask, slices.Concat(claudeAskRules, claudeOldAskRules))
		if len(keep) < len(ask) {
			changed = true
			if len(keep) > 0 {
				perms["ask"] = keep
			} else {
				delete(perms, "ask")
			}
		}
		if changed && len(perms) == 0 {
			delete(m, "permissions")
		}
	}

	if !changed {
		return false, nil
	}
	if len(m) == 0 {
		if err := r.Remove(path); err != nil {
			return false, err
		}
		r.Remove(filepath.Dir(path)) // only if empty
		return true, nil
	}
	return true, writeSettings(r, path, m)
}

// installClaude and uninstallClaude report the result of the merge; a file
// they can't edit is left untouched with instructions instead.
func installClaude(r *os.Root) {
	changed, err := mergeClaudeSettings(r, claudeSettings)
	switch {
	case errors.Is(err, errClaudeSettings):
		fmt.Printf("%s is not valid settings JSON; left it untouched. For the Claude Code sandbox hook, merge in:\n%s\n", claudeSettings, claudeSnippet())
	case err != nil:
		fmt.Printf("could not update %s (%v); merge in by hand:\n%s\n", claudeSettings, confined(claudeSettings, err), claudeSnippet())
	case changed:
		fmt.Printf("added the Claude Code sandbox hooks and ask rules to %s\n", claudeSettings)
	}
}

func uninstallClaude(r *os.Root) {
	changed, err := removeClaudeSettings(r, claudeSettings)
	switch {
	case err != nil:
		fmt.Printf("could not clean %s (%v); remove the ddev-bridge hooks and ask rules by hand:\n%s\n", claudeSettings, err, claudeSnippet())
	case changed:
		fmt.Printf("removed the Claude Code sandbox hooks and ask rules from %s\n", claudeSettings)
	}
}
