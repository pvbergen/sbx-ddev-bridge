package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The sandbox controls the project tree. A symlink out of it must make the
// install fail instead of writing elsewhere on the host.

func openProject(t *testing.T) (string, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return dir, r
}

func TestInstallClientInstallsLinks(t *testing.T) {
	dir, r := openProject(t)
	client := filepath.Join(t.TempDir(), "client")
	os.WriteFile(client, []byte("bin"), 0o755)
	for i := 0; i < 2; i++ { // the second run replaces the links
		if err := installClient(r, client); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"ddev", "drush"} {
		if target, err := os.Readlink(filepath.Join(dir, BinDir, n)); err != nil || target != "ddev-bridge-client" {
			t.Fatalf("%s -> %q, %v", n, target, err)
		}
	}
	removeClient(r)
	if _, err := os.Lstat(filepath.Join(dir, ".ddev-bridge")); !os.IsNotExist(err) {
		t.Fatalf(".ddev-bridge left behind: %v", err)
	}
}

func TestInstallClientRefusesSymlinkOut(t *testing.T) {
	client := filepath.Join(t.TempDir(), "client")
	os.WriteFile(client, []byte("bin"), 0o755)
	for _, link := range []string{".ddev-bridge", ".ddev-bridge/bin"} {
		t.Run(link, func(t *testing.T) {
			dir, r := openProject(t)
			outside := t.TempDir()
			os.MkdirAll(filepath.Dir(filepath.Join(dir, link)), 0o755)
			if err := os.Symlink(outside, filepath.Join(dir, link)); err != nil {
				t.Fatal(err)
			}
			if err := installClient(r, client); err == nil {
				t.Fatal("install through a symlink out of the project must fail")
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Fatalf("wrote outside the project: %v", entries)
			}
		})
	}
}

func TestProjectFilesRefuseSymlinkOut(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "victim")
	orig := []byte("{\"keep\": true}\n")

	for name, run := range map[string]func(r *os.Root, path string) error{
		claudeSettings: func(r *os.Root, p string) error { _, err := mergeClaudeSettings(r, p); return err },
		".gitignore":   func(r *os.Root, p string) error { _, err := removeGitignore(r, p); return err },
	} {
		t.Run(name, func(t *testing.T) {
			os.WriteFile(outside, orig, 0o644)
			dir, r := openProject(t)
			os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
			if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if err := run(r, name); err == nil {
				t.Fatal("write through a symlink out of the project must fail")
			}
			if b, _ := os.ReadFile(outside); string(b) != string(orig) {
				t.Fatalf("file outside the project changed: %q", b)
			}
		})
	}
}
