package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// excludeLines keep the per-developer files out of git without touching a
// tracked file: .git/info/exclude belongs to the clone and is never committed.
var excludeLines = []string{"/.ddev-bridge/", "/.claude/settings.local.json"}

const (
	excludeBegin = "# ddev-bridge: begin (per-developer install; removed by `ddev bridge disable`)"
	excludeEnd   = "# ddev-bridge: end"
)

// errNoGit means the project is not a git checkout, so there is nothing to ignore.
var errNoGit = errors.New("not a git repository")

// gitCommonDir finds the directory holding info/exclude for the checkout in
// root, without running git: git would read the project's .git/config, which
// the sandbox can edit (core.fsmonitor and friends run programs).
//   - .git is a directory: that directory.
//   - .git is a file (worktree, submodule): "gitdir: <dir>", and for a worktree
//     <dir>/commondir names the main repository's git dir.
//
// The result must look like a git dir (HEAD and objects/), so a forged .git
// file can at most point at another repository.
func gitCommonDir(root string) (string, error) {
	dotgit := filepath.Join(root, ".git")
	st, err := os.Lstat(dotgit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", errNoGit
	case err != nil:
		return "", err
	case st.Mode()&fs.ModeSymlink != 0:
		return "", fmt.Errorf(".git is a symlink; not following it")
	}
	dir := dotgit
	if !st.IsDir() {
		b, err := os.ReadFile(dotgit)
		if err != nil {
			return "", err
		}
		gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		if !ok {
			return "", fmt.Errorf(".git is a file without a gitdir line")
		}
		dir = resolve(root, strings.TrimSpace(gitdir))
		if c, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
			dir = resolve(dir, strings.TrimSpace(string(c)))
		}
	}
	for _, p := range []string{"HEAD", "objects"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			return "", fmt.Errorf("%s doesn't look like a git directory: %w", dir, err)
		}
	}
	return dir, nil
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// excludePath is info/exclude relative to the git dir.
var excludePath = filepath.Join("info", "exclude")

// ensureExclude writes our block to info/exclude, replacing an older one.
// Writes go through an os.Root on the git dir, so a symlinked info/exclude
// can't redirect them.
func ensureExclude(gitDir string) (bool, error) {
	r, err := os.OpenRoot(gitDir)
	if err != nil {
		return false, err
	}
	defer r.Close()
	data, err := r.ReadFile(excludePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	rest, _ := cutExcludeBlock(string(data))
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	out := rest + excludeBegin + "\n" + strings.Join(excludeLines, "\n") + "\n" + excludeEnd + "\n"
	if out == string(data) {
		return false, nil
	}
	if err := r.MkdirAll("info", 0o755); err != nil {
		return false, err
	}
	return true, r.WriteFile(excludePath, []byte(out), 0o644)
}

// removeExclude deletes our block from info/exclude and keeps everything else.
func removeExclude(gitDir string) (bool, error) {
	r, err := os.OpenRoot(gitDir)
	if err != nil {
		return false, err
	}
	defer r.Close()
	data, err := r.ReadFile(excludePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	rest, found := cutExcludeBlock(string(data))
	if !found {
		return false, nil
	}
	return true, r.WriteFile(excludePath, []byte(rest), 0o644)
}

// cutExcludeBlock returns s without the lines from excludeBegin to excludeEnd.
// A begin without an end (hand-edited) cuts to the end of the file.
func cutExcludeBlock(s string) (string, bool) {
	lines := strings.SplitAfter(s, "\n")
	var keep []string
	in, found := false, false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case !in && t == excludeBegin:
			in, found = true, true
		case in && t == excludeEnd:
			in = false
		case !in:
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, ""), found
}

// ignoreLocally keeps the bridge's files out of git for the project in root
// (r): our block in info/exclude, and the lines older versions wrote to the
// tracked .gitignore removed. It reports what it did to say.
func ignoreLocally(root string, r *os.Root, say func(string)) error {
	gitDir, err := gitCommonDir(root)
	switch {
	case errors.Is(err, errNoGit):
		// nothing to ignore
	case err != nil:
		return fmt.Errorf("can't add the ignore lines to .git/info/exclude: %w", err)
	default:
		if added, err := ensureExclude(gitDir); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(gitDir, excludePath), err)
		} else if added {
			say("ignored " + strings.Join(excludeLines, " and ") + " in " + filepath.Join(gitDir, excludePath) + " (this clone only)")
		}
	}
	if removed, err := removeGitignore(r, ".gitignore"); err != nil {
		say(fmt.Sprintf("could not clean .gitignore (%v); remove the ddev-bridge lines by hand", confined(".gitignore", err)))
	} else if removed {
		say("removed the ddev-bridge lines from .gitignore (now in .git/info/exclude); commit that change")
	}
	return nil
}

// unignoreLocally reverses ignoreLocally. Worktrees share info/exclude, so
// the block stays while another enabled project (in others) uses the same
// repository.
func unignoreLocally(root string, r *os.Root, others []string, say func(string)) {
	if gitDir, err := gitCommonDir(root); err == nil {
		exclude := filepath.Join(gitDir, excludePath)
		if o := sameRepo(gitDir, others); o != "" {
			say("kept the ddev-bridge block in " + exclude + ": " + o + " is still enabled")
		} else if removed, err := removeExclude(gitDir); err != nil {
			say(fmt.Sprintf("could not clean %s (%v); remove the ddev-bridge block by hand", exclude, err))
		} else if removed {
			say("removed the ddev-bridge block from " + exclude)
		}
	}
	if removed, err := removeGitignore(r, ".gitignore"); err != nil {
		say(fmt.Sprintf("could not clean .gitignore (%v); remove the ddev-bridge lines by hand", confined(".gitignore", err)))
	} else if removed {
		say("removed the ddev-bridge lines from .gitignore")
	}
}

// sameRepo returns the first of roots whose git dir is gitDir, or "".
func sameRepo(gitDir string, roots []string) string {
	for _, o := range roots {
		if d, err := gitCommonDir(o); err == nil && d == gitDir {
			return o
		}
	}
	return ""
}
