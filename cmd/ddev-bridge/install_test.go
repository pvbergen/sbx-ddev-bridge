package main

import (
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
	want := "vendor/\n# ddev-bridge (sandbox client and token)\n/.helga-ai/bin/\n/.helga-ai/ddev-bridge.token\n"
	if string(b) != want {
		t.Fatalf("got %q, want %q", b, want)
	}

	// A broader existing rule is respected.
	q := filepath.Join(t.TempDir(), ".gitignore")
	os.WriteFile(q, []byte(".helga-ai/\n"), 0o644)
	if added, _ := ensureGitignore(q); added {
		t.Fatal(".helga-ai/ already ignored; nothing to add")
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
