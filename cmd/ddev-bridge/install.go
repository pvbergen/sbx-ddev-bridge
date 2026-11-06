package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"ddev-bridge/internal/registry"
)

// BinDir is where the sandbox client and its symlinks live in a project.
var BinDir = filepath.Join(registry.Dir, "bin")

// legacyGitignore are the lines earlier versions added to the tracked
// .gitignore (the last two with the per-project add-on). The ignore lines now
// go to .git/info/exclude; enable, disable and setup remove these.
var legacyGitignore = []string{
	"# ddev-bridge (per-developer install, not for the repository)",
	"/" + registry.Dir + "/",
	"/.ddev/config.ddev-bridge.yaml",
	"/.ddev/addon-metadata/ddev-bridge/",
}

// defaultClient is the client next to this host binary in the package layout:
// <dist>/host/<os>-<arch>/ddev-bridge -> <dist>/client/linux-<arch>/ddev-bridge-client.
// Sandboxes run Linux on the host's CPU architecture.
func defaultClient() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Join(filepath.Dir(exe), "..", "..", "client", "linux-"+runtime.GOARCH, "ddev-bridge-client")
}

// enableProject puts the client into <root>/.ddev-bridge/bin, ignores it in
// .git/info/exclude, updates the Claude Code settings, registers the project, approves its .ddev/ and
// starts the bridge.
func enableProject(args []string) {
	fs := flag.NewFlagSet("enable", flag.ExitOnError)
	client := fs.String("client", defaultClient(), "sandbox client binary to install")
	noSbx := fs.Bool("no-sbx", false, "do not add the sbx network rule")
	pos := mustParse(fs, args, 1, "[flags] [dir]")
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	root, _, err := registry.ValidateRoot(dir)
	if err != nil {
		fatal(err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		fatal(err)
	}
	defer r.Close()

	if err := installClient(r, *client); err != nil {
		fatal(err)
	}
	fmt.Printf("installed sandbox client in %s (ddev, drush)\n", filepath.Join(root, BinDir))

	if err := ignoreLocally(root, r, func(s string) { fmt.Println(s) }); err != nil {
		fatal(err)
	}

	installClaude(r)

	reg, err := registry.Load()
	if err != nil {
		fatal(err)
	}
	p, err := reg.Register(root)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("registered %q; token in %s, .ddev/ approved\n", p.Name, registry.TokenFile)

	if !*noSbx {
		sbxAllow(os.Stdout)
	}
	if err := ensure(defaultAddr); err != nil {
		fatal(err)
	}
}

// disableProject reverses enableProject, including the ignore lines and the
// Claude Code settings. The bridge keeps running for other projects.
func disableProject(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		fatal(err)
	}
	reg, err := registry.Load()
	if err != nil {
		fatal(err)
	}
	if err := reg.Unregister(root); err != nil {
		fmt.Println(err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		fmt.Printf("%v; nothing to remove in the project\n", err)
		return
	}
	defer r.Close()
	removeClient(r)
	fmt.Println("removed the sandbox client and token, unregistered the project")
	var others []string
	for _, p := range reg.Projects {
		others = append(others, p.Root)
	}
	unignoreLocally(root, r, others, func(s string) { fmt.Println(s) })
	uninstallClaude(r)
}

// installClient copies the client into .ddev-bridge/bin and points the ddev
// and drush links at it. All writes go through r: the sandbox controls the
// project tree, and a symlink out of it (e.g. .ddev-bridge/bin -> ~/bin) must
// fail instead of writing elsewhere on the host.
func installClient(r *os.Root, client string) error {
	if err := r.MkdirAll(BinDir, 0o755); err != nil {
		return confined(BinDir, err)
	}
	if err := copyAtomic(r, client, filepath.Join(BinDir, "ddev-bridge-client")); err != nil {
		return confined(BinDir, fmt.Errorf("install client: %w", err))
	}
	for _, name := range []string{"ddev", "drush"} {
		link := filepath.Join(BinDir, name)
		if err := r.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return confined(link, err)
		}
		if err := r.Symlink("ddev-bridge-client", link); err != nil {
			return confined(link, err)
		}
	}
	return nil
}

// removeClient deletes the client, its links and the token, and the
// directories if they end up empty. Errors are ignored: whatever is left is
// harmless, and r keeps the removals inside the project.
func removeClient(r *os.Root) {
	for _, n := range []string{"ddev", "drush", "ddev-bridge-client"} {
		r.Remove(filepath.Join(BinDir, n))
	}
	r.Remove(BinDir)
	r.Remove(registry.TokenFile)
	r.Remove(registry.Dir) // only if empty
}

// confined explains an error from a write through os.Root, which most often
// means a symlink that leads out of the project.
func confined(path string, err error) error {
	return fmt.Errorf("%s: %w (writes stay inside the project; is a part of this path a symlink out of it?)", path, err)
}

// removeGitignore deletes the lines earlier versions added (legacyGitignore,
// exactly as written) and keeps everything else. The file is kept even if it
// ends up empty, because it may be tracked.
func removeGitignore(r *os.Root, path string) (bool, error) {
	data, err := r.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	ours := map[string]bool{}
	for _, l := range legacyGitignore {
		ours[l] = true
	}
	trailing := strings.HasSuffix(string(data), "\n")
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var keep []string
	for _, l := range lines {
		if !ours[strings.TrimSpace(l)] {
			keep = append(keep, l)
		}
	}
	if len(keep) == len(lines) {
		return false, nil
	}
	out := strings.Join(keep, "\n")
	if trailing && out != "" {
		out += "\n"
	}
	return true, r.WriteFile(path, []byte(out), 0o644)
}

// copyAtomic copies src (a host path) to dst inside r via a temp file and
// rename, so a client that is running in a sandbox is never overwritten in place.
func copyAtomic(r *os.Root, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	b := make([]byte, 8)
	rand.Read(b)
	tmpName := filepath.Join(filepath.Dir(dst), ".ddev-bridge-client-"+hex.EncodeToString(b))
	tmp, err := r.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer r.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return r.Rename(tmpName, dst)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ddev-bridge:", err)
	os.Exit(1)
}
