package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitDir makes dir look like a git directory.
func fakeGitDir(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "objects"), 0o755)
	os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
}

func TestGitCommonDir(t *testing.T) {
	root := t.TempDir()
	if _, err := gitCommonDir(root); !errors.Is(err, errNoGit) {
		t.Fatalf("no .git: want errNoGit, got %v", err)
	}

	fakeGitDir(t, filepath.Join(root, ".git"))
	if d, err := gitCommonDir(root); err != nil || d != filepath.Join(root, ".git") {
		t.Fatalf(".git dir: %q %v", d, err)
	}

	// Worktree: .git file -> <main>/.git/worktrees/wt, whose commondir is ../..
	main := filepath.Join(t.TempDir(), ".git")
	fakeGitDir(t, main)
	wtGit := filepath.Join(main, "worktrees", "wt")
	os.MkdirAll(wtGit, 0o755)
	os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644)
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644)
	if d, err := gitCommonDir(wt); err != nil || d != main {
		t.Fatalf("worktree: %q %v, want %q", d, err, main)
	}

	// A .git file pointing at something that isn't a git dir, and a symlinked .git.
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, ".git"), []byte("gitdir: "+t.TempDir()+"\n"), 0o644)
	if _, err := gitCommonDir(bad); err == nil {
		t.Fatal("gitdir without HEAD/objects must be refused")
	}
	linked := t.TempDir()
	os.Symlink(main, filepath.Join(linked, ".git"))
	if _, err := gitCommonDir(linked); err == nil {
		t.Fatal("symlinked .git must be refused")
	}
}

func TestEnsureAndRemoveExclude(t *testing.T) {
	gitDir := t.TempDir()
	exclude := filepath.Join(gitDir, "info", "exclude")

	// No info/ yet.
	if added, err := ensureExclude(gitDir); err != nil || !added {
		t.Fatalf("first: added=%v err=%v", added, err)
	}
	if removed, err := removeExclude(gitDir); err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	if b := must(os.ReadFile(exclude)); len(b) != 0 {
		t.Fatalf("want empty, got %q", b)
	}

	orig := "# git ls-files --others --exclude-from=.git/info/exclude\n*.local" // no trailing newline
	os.WriteFile(exclude, []byte(orig), 0o644)
	if added, err := ensureExclude(gitDir); err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	if added, _ := ensureExclude(gitDir); added {
		t.Fatal("second call must be a no-op")
	}
	got := string(must(os.ReadFile(exclude)))
	for _, l := range excludeLines {
		if !strings.Contains(got, "\n"+l+"\n") {
			t.Fatalf("missing %q in %q", l, got)
		}
	}

	// An older block is replaced, lines added after it are kept.
	old := strings.Replace(got, excludeLines[1]+"\n", "", 1) + "/tmp/\n"
	os.WriteFile(exclude, []byte(old), 0o644)
	if added, _ := ensureExclude(gitDir); !added {
		t.Fatal("outdated block must be rewritten")
	}
	if got := string(must(os.ReadFile(exclude))); strings.Count(got, excludeBegin) != 1 || !strings.Contains(got, "/tmp/\n") {
		t.Fatalf("got %q", got)
	}

	if removed, _ := removeExclude(gitDir); !removed {
		t.Fatal("block not removed")
	}
	if got := string(must(os.ReadFile(exclude))); got != orig+"\n/tmp/\n" {
		t.Fatalf("got %q", got)
	}
	if removed, _ := removeExclude(gitDir); removed {
		t.Fatal("second removal must be a no-op")
	}
}

func TestExcludeRefusesSymlinkOut(t *testing.T) {
	gitDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(outside, []byte("keep\n"), 0o644)
	os.MkdirAll(filepath.Join(gitDir, "info"), 0o755)
	os.Symlink(outside, filepath.Join(gitDir, "info", "exclude"))
	if _, err := ensureExclude(gitDir); err == nil {
		t.Fatal("write through a symlinked info/exclude must fail")
	}
	if b := must(os.ReadFile(outside)); string(b) != "keep\n" {
		t.Fatalf("file outside changed: %q", b)
	}
}

// TestIgnoreLocallyWithGit checks the result with git itself, in a clone and
// a worktree, including the move from .gitignore.
func TestIgnoreLocallyWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	repo := t.TempDir()
	git(repo, "init", "-q")
	legacy := "vendor/\n# ddev-bridge (per-developer install, not for the repository)\n/.ddev-bridge/\n"
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(legacy), 0o644)
	git(repo, "add", ".gitignore")
	git(repo, "commit", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", wt)

	for _, dir := range []string{repo, wt} {
		r, _ := os.OpenRoot(dir)
		if err := ignoreLocally(dir, r, func(string) {}); err != nil {
			t.Fatal(err)
		}
		r.Close()
		if b := must(os.ReadFile(filepath.Join(dir, ".gitignore"))); string(b) != "vendor/\n" {
			t.Fatalf("%s: legacy lines left in .gitignore: %q", dir, b)
		}
		os.MkdirAll(filepath.Join(dir, ".ddev-bridge"), 0o755)
		os.WriteFile(filepath.Join(dir, ".ddev-bridge", "token"), []byte("x"), 0o600)
		os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
		os.WriteFile(filepath.Join(dir, ".claude", "settings.local.json"), []byte("{}"), 0o644)
		if st := git(dir, "status", "--porcelain"); st != " M .gitignore\n" {
			t.Fatalf("%s: want only the .gitignore change, got %q", dir, st)
		}
	}

	r, _ := os.OpenRoot(repo)
	defer r.Close()
	// The worktree is still enabled: its files stay ignored.
	unignoreLocally(repo, r, []string{wt}, func(string) {})
	if st := git(wt, "status", "--porcelain", "--untracked-files=all"); strings.Contains(st, ".ddev-bridge") {
		t.Fatalf("block removed although the worktree is enabled: %q", st)
	}
	unignoreLocally(repo, r, nil, func(string) {})
	if st := git(repo, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(st, "?? .ddev-bridge/token") {
		t.Fatalf("after disable the files must show up again, got %q", st)
	}
}
