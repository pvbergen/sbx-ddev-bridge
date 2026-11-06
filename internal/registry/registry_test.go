package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func project(t *testing.T) string {
	t.Helper()
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".ddev"), 0o755)
	os.WriteFile(filepath.Join(dir, ".ddev", "config.yaml"), []byte("name: p\n"), 0o644)
	return dir
}

func TestRegisterWritesToken(t *testing.T) {
	dir := project(t)
	reg, _ := Load()
	p, err := reg.Register(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, TokenFile))
	if err != nil || len(b) != 65 {
		t.Fatalf("token %q, %v", b, err)
	}
	if got, ok := reg.Lookup(string(b[:64])); !ok || got.Root != p.Root {
		t.Fatal("token does not resolve to the project")
	}
}

// The sandbox controls the project tree: a token or .ddev-bridge/ symlinked
// out of it must not redirect the token write to a host file.
func TestRegisterRefusesTokenSymlinkOut(t *testing.T) {
	for _, link := range []string{Dir, TokenFile} {
		t.Run(link, func(t *testing.T) {
			dir := project(t)
			outside := filepath.Join(t.TempDir(), "victim")
			os.WriteFile(outside, []byte("keep\n"), 0o644)
			target := outside
			if link == Dir {
				target = filepath.Dir(outside)
			}
			os.MkdirAll(filepath.Dir(filepath.Join(dir, link)), 0o755)
			if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
				t.Fatal(err)
			}
			reg, _ := Load()
			if _, err := reg.Register(dir); err == nil {
				t.Fatal("register through a symlink out of the project must fail")
			}
			if b, _ := os.ReadFile(outside); string(b) != "keep\n" {
				t.Fatalf("host file changed: %q", b)
			}
			if entries, _ := os.ReadDir(filepath.Dir(outside)); len(entries) != 1 {
				t.Fatalf("wrote next to the host file: %v", entries)
			}
			if len(reg.Projects) != 0 {
				t.Fatal("project registered despite the failed token write")
			}
		})
	}
}
