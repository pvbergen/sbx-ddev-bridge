package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGitignore(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".gitignore")
	os.WriteFile(p, []byte("vendor/"), 0o644) // no trailing newline
	if added, err := ensureGitignore(p); err != nil || !added {
		t.Fatalf("first call: added=%v err=%v", added, err)
	}
	if added, _ := ensureGitignore(p); added {
		t.Fatal("second call must be a no-op")
	}
	b, _ := os.ReadFile(p)
	want := "vendor/\n# ddev-bridge (per-developer install, not for the repository)\n/.ddev-bridge/\n/.ddev/config.ddev-bridge.yaml\n/.ddev/addon-metadata/ddev-bridge/\n"
	if string(b) != want {
		t.Fatalf("got %q, want %q", b, want)
	}

	// Existing entries, anchored or not, are respected.
	q := filepath.Join(t.TempDir(), ".gitignore")
	os.WriteFile(q, []byte(".ddev-bridge/\n.ddev/config.ddev-bridge.yaml\n/.ddev/addon-metadata/ddev-bridge/\n"), 0o644)
	if added, _ := ensureGitignore(q); added {
		t.Fatal("all entries already present; nothing to add")
	}

	// An older install (client dir only) gets just the missing lines.
	r := filepath.Join(t.TempDir(), ".gitignore")
	os.WriteFile(r, []byte("/.ddev-bridge/\n"), 0o644)
	if added, _ := ensureGitignore(r); !added {
		t.Fatal("the add-on config lines are missing and must be added")
	}
	b, _ = os.ReadFile(r)
	if strings.Count(string(b), "/.ddev-bridge/") != 1 || !strings.Contains(string(b), "/.ddev/config.ddev-bridge.yaml\n") {
		t.Fatalf("unexpected .gitignore: %q", b)
	}
}

func TestCopyAtomicReplacesRunningFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("new"), 0o644)
	os.WriteFile(dst, []byte("old"), 0o755)
	old, _ := os.Open(dst) // stands in for a running binary
	defer old.Close()
	if err := copyAtomic(src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	st, _ := os.Stat(dst)
	if string(b) != "new" || st.Mode().Perm() != 0o755 {
		t.Fatalf("dst = %q %v", b, st.Mode())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ddev-bridge-client-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestParseFlags(t *testing.T) {
	newFS := func() (*flag.FlagSet, *bool) {
		fs := flag.NewFlagSet("install-project", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return fs, fs.Bool("no-sbx", false, "")
	}

	fs, noSbx := newFS()
	pos, err := parseFlags(fs, []string{"--no-sbx", "/p"}, 1)
	if err != nil || !*noSbx || len(pos) != 1 || pos[0] != "/p" {
		t.Fatalf("flags first: pos=%v noSbx=%v err=%v", pos, *noSbx, err)
	}

	fs, _ = newFS()
	if _, err := parseFlags(fs, []string{"/p", "--no-sbx"}, 1); err == nil || !strings.Contains(err.Error(), "--no-sbx") {
		t.Fatalf("flag after the dir must fail and name the flag, got %v", err)
	}

	fs, _ = newFS()
	if _, err := parseFlags(fs, []string{"/p", "/q"}, 1); err == nil {
		t.Fatal("a second positional argument must fail")
	}

	fs, _ = newFS()
	if pos, err := parseFlags(fs, nil, 1); err != nil || len(pos) != 0 {
		t.Fatalf("no args: pos=%v err=%v", pos, err)
	}
}

func TestRemoveGitignore(t *testing.T) {
	// It reverses ensureGitignore and keeps every other line.
	p := filepath.Join(t.TempDir(), ".gitignore")
	orig := "vendor/\n# local\n.env\n"
	os.WriteFile(p, []byte(orig), 0o644)
	ensureGitignore(p)
	os.WriteFile(p, append(must(os.ReadFile(p)), "node_modules/\n"...), 0o644) // a line added later
	if removed, err := removeGitignore(p); err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if got := string(must(os.ReadFile(p))); got != orig+"node_modules/\n" {
		t.Fatalf("got %q", got)
	}

	// No block, no change; a missing file is fine.
	if removed, _ := removeGitignore(p); removed {
		t.Fatal("second call must be a no-op")
	}
	if removed, err := removeGitignore(filepath.Join(t.TempDir(), ".gitignore")); removed || err != nil {
		t.Fatalf("missing file: removed=%v err=%v", removed, err)
	}

	// A .gitignore holding only our block is emptied, not deleted.
	q := filepath.Join(t.TempDir(), ".gitignore")
	ensureGitignore(q)
	removeGitignore(q)
	if b, err := os.ReadFile(q); err != nil || len(b) != 0 {
		t.Fatalf("want an empty file, got %q err=%v", b, err)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
