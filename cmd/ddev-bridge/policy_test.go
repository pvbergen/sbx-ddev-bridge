package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyCmd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", filepath.Join(dir, "cfg"))
	proj := filepath.Join(dir, "proj")
	os.MkdirAll(filepath.Join(proj, ".ddev"), 0o755)
	os.WriteFile(filepath.Join(proj, ".ddev", "config.yaml"), []byte("name: demo\n"), 0o644)

	run := func(args ...string) (string, int) {
		var out bytes.Buffer
		code := policyCmd(append(args[:1:1], append([]string{"--dir", proj}, args[1:]...)...), strings.NewReader(""), &out)
		return out.String(), code
	}
	for _, c := range []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"deny", "--reason", "ssh to live", "ddev", "get_dump"}, 0, `added "deny ddev get_dump" in [demo]`},
		{[]string{"deny", "--global", "drush sql:drop"}, 0, `added "deny drush sql:drop" in global`},
		{[]string{"allow", "drush", "%"}, 0, "overrides built-in denials, e.g. drush @remote"},
		{[]string{"allow", "drush", "*"}, 1, ""},
		{[]string{"deny", "ddev", "x", "--global"}, 2, ""}, // flags go first
		{[]string{"ls", "--global"}, 2, ""},
		{[]string{"ls"}, 0, "  1  deny  drush sql:drop  (line 1)\n[demo]\n  2  deny  ddev get_dump  # ssh to live"},
		{[]string{"rm"}, 1, ""}, // no number and no terminal
		{[]string{"rm", "3"}, 0, `removed "allow drush %" from [demo]`},
		{[]string{"rm", "3"}, 1, ""},
	} {
		out, code := run(c.args...)
		if code != c.code || !strings.Contains(out, c.out) {
			t.Errorf("%q: exit %d, output:\n%s", c.args, code, out)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "cfg", "policy"))
	if string(b) != "deny drush sql:drop\n\n[demo]\ndeny ddev get_dump  # ssh to live\n" {
		t.Errorf("file:\n%s", b)
	}
}
