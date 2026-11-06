package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"ddev-bridge/internal/registry"
)

// Machine-wide install: the binaries and the `ddev bridge` host command live in
// ddev's global dir, and a global post-start hook starts the bridge for enabled
// projects. Nothing of the bridge goes into a project's .ddev/.

const (
	distDir     = "ddev-bridge-dist"
	hostCommand = "commands/host/bridge"
	globalCfg   = "global_config.yaml"
	hookTask    = "ddev bridge ensure"
)

// hookBlock is what setup appends to global_config.yaml. ddev rewrites the
// file with yaml.Marshal (e.g. on `ddev config global`), which drops the
// comment and re-indents, so the task line itself is what we look for later.
const hookBlock = `# ddev-bridge: start the bridge for enabled projects (removed by ddev-bridge uninstall)
hooks:
  post-start:
    - exec-host: ` + hookTask + `
`

var (
	hookTaskRe = regexp.MustCompile(`^\s*-\s*exec-host:\s*(["']?)` + regexp.QuoteMeta(hookTask) + `(["']?)\s*$`)
	hooksKeyRe = regexp.MustCompile(`^hooks\s*:`)
)

// errOtherHooks means global_config.yaml already has hooks of its own; we
// don't merge into YAML we didn't write.
var errOtherHooks = errors.New("has hooks of its own")

// addGlobalHook returns data with the bridge hook appended. It changes nothing
// if the hook is already there, and fails if other hooks exist.
func addGlobalHook(data []byte) ([]byte, bool, error) {
	lines := strings.Split(string(data), "\n")
	hasHooks := false
	for _, l := range lines {
		if hookTaskRe.MatchString(l) {
			return data, false, nil
		}
		hasHooks = hasHooks || hooksKeyRe.MatchString(l)
	}
	if hasHooks {
		return data, false, errOtherHooks
	}
	var out bytes.Buffer
	out.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		out.WriteByte('\n')
	}
	if len(data) > 0 {
		out.WriteByte('\n')
	}
	out.WriteString(hookBlock)
	return out.Bytes(), true, nil
}

// removeGlobalHook drops the bridge's task line, then each parent (`post-start:`,
// `hooks:`) left without children, and our comment above `hooks:`. It works on
// the block as setup writes it and as ddev re-marshals it.
func removeGlobalHook(data []byte) ([]byte, bool) {
	lines := strings.Split(string(data), "\n")
	at := -1
	for i, l := range lines {
		if hookTaskRe.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		return data, false
	}
	child := lines[at]
	lines = remove(lines, at)
	for p := at - 1; p >= 0; p-- {
		if !isContent(lines[p]) || !isChild(lines[p], child) {
			continue
		}
		if hasChildren(lines, p) {
			break
		}
		child = lines[p]
		lines = remove(lines, p)
		if leading(child) > 0 {
			continue
		}
		// A top-level key went: drop our comment and the blank line before it.
		if p > 0 && strings.HasPrefix(lines[p-1], "# ddev-bridge:") {
			p--
			lines = remove(lines, p)
		}
		if p > 0 && strings.TrimSpace(lines[p-1]) == "" && (p == len(lines) || strings.TrimSpace(lines[p]) == "") {
			lines = remove(lines, p-1)
		}
		break
	}
	return []byte(strings.Join(lines, "\n")), true
}

func remove(lines []string, i int) []string {
	return append(lines[:i:i], lines[i+1:]...)
}

func isContent(l string) bool {
	t := strings.TrimSpace(l)
	return t != "" && !strings.HasPrefix(t, "#")
}

func leading(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }

// isChild reports whether line belongs under the key line parent: more
// indented, or a list item at the key's own indentation (compact YAML).
func isChild(parent, line string) bool {
	if leading(line) > leading(parent) {
		return true
	}
	return leading(line) == leading(parent) &&
		strings.HasPrefix(strings.TrimSpace(line), "- ") &&
		strings.HasSuffix(strings.TrimSpace(parent), ":")
}

// hasChildren reports whether the content line after p belongs under it.
func hasChildren(lines []string, p int) bool {
	for _, l := range lines[p+1:] {
		if isContent(l) {
			return isChild(lines[p], l)
		}
	}
	return false
}

// writeKeepingLink writes data to path atomically. If path is a symlink (a
// dotfiles setup), the target is replaced, not the link.
func writeKeepingLink(path string, data []byte) error {
	if t, err := filepath.EvalSymlinks(path); err == nil {
		path = t
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ddevGlobalDir mirrors ddev's GetGlobalDdevDirLocation (v1.25.5). install.sh
// passes ddev's own answer via --global-dir; this is the fallback.
func ddevGlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if x := os.Getenv("DDEV_XDG_CONFIG_HOME"); x != "" {
		if strings.HasPrefix(x, "~") {
			x = home + x[1:]
		}
		return filepath.Join(x, "ddev"), nil
	}
	dot := filepath.Join(home, ".ddev")
	if st, err := os.Stat(dot); runtime.GOOS == "linux" && (err != nil || !st.IsDir()) {
		if _, err := os.Stat(filepath.Join(home, ".config", "ddev")); err == nil {
			return filepath.Join(home, ".config", "ddev"), nil
		}
	}
	return dot, nil
}

// packageRoot is the unpacked tarball this binary runs from:
// <root>/ddev-bridge-dist/host/<os>-<arch>/ddev-bridge.
func packageRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "..", ".."))
}

type setupOpts struct {
	src, globalDir string
	addr           string
	noSbx          bool
	daemon         bool // stop and start the bridge (off in tests)
}

func setupCmd(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	o := setupOpts{addr: defaultAddr, daemon: true}
	fs.StringVar(&o.globalDir, "global-dir", "", "ddev's global config dir (default: as ddev finds it)")
	fs.StringVar(&o.src, "from", packageRoot(), "unpacked ddev-bridge package to install")
	fs.BoolVar(&o.noSbx, "no-sbx", false, "do not add the sbx network rule")
	mustParse(fs, args, 0, "[--global-dir dir] [--no-sbx]")
	if o.globalDir == "" {
		d, err := ddevGlobalDir()
		if err != nil {
			fatal(err)
		}
		o.globalDir = d
	}
	if err := runSetup(o, os.Stdout); err != nil {
		fatal(err)
	}
}

// runSetup installs or updates the machine-wide bridge. Re-running it updates.
func runSetup(o setupOpts, out io.Writer) error {
	srcDist := filepath.Join(o.src, distDir)
	srcCmd := filepath.Join(o.src, filepath.FromSlash(hostCommand))
	for _, p := range []string{srcDist, srcCmd} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s is not an unpacked ddev-bridge package (%v)", o.src, err)
		}
	}
	if st, err := os.Stat(o.globalDir); err != nil || !st.IsDir() {
		return fmt.Errorf("ddev global dir %s not found; run ddev once, or pass --global-dir", o.globalDir)
	}

	// Check the hook first, so a config we can't edit stops setup before
	// anything is replaced.
	cfgPath := filepath.Join(o.globalDir, globalCfg)
	cfg, err := os.ReadFile(cfgPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	newCfg, hookChanged, err := addGlobalHook(cfg)
	if errors.Is(err, errOtherHooks) {
		return fmt.Errorf("%s %v; add this task under `hooks:` by hand and run setup again:\n  post-start:\n    - exec-host: %s", cfgPath, err, hookTask)
	}

	reg, err := registry.Load()
	if err != nil {
		return err
	}
	warnLegacyAddon(reg, out)

	dstDist := filepath.Join(o.globalDir, distDir)
	if !sameDir(srcDist, dstDist) {
		if o.daemon {
			if err := stop(o.addr); err != nil {
				return err
			}
		}
		if err := replaceTree(srcDist, dstDist); err != nil {
			return fmt.Errorf("install %s: %w", dstDist, err)
		}
		if err := installFile(srcCmd, filepath.Join(o.globalDir, filepath.FromSlash(hostCommand)), 0o755); err != nil {
			return fmt.Errorf("install the host command: %w", err)
		}
		fmt.Fprintf(out, "installed ddev-bridge into %s\n", o.globalDir)
	}
	if hookChanged {
		if err := writeKeepingLink(cfgPath, newCfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "added the post-start hook to %s\n", cfgPath)
	}

	if !o.noSbx {
		sbxAllow(out)
	}
	if o.daemon {
		bin := filepath.Join(dstDist, "host", runtime.GOOS+"-"+runtime.GOARCH, "ddev-bridge")
		cmd := exec.Command(bin, "ensure", "--addr", o.addr)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("start the bridge: %w", err)
		}
	}

	client := filepath.Join(dstDist, "client", "linux-"+runtime.GOARCH, "ddev-bridge-client")
	return refreshClients(reg, client, out)
}

// refreshClients updates the client in every registered project, so client
// and bridge versions match. Only the client: tokens and approvals stay.
func refreshClients(reg *registry.Registry, client string, out io.Writer) error {
	failed := 0
	for _, p := range reg.Projects {
		r, err := os.OpenRoot(p.Root)
		if err != nil {
			fmt.Fprintf(out, "%s: project root %s is gone; `ddev-bridge disable %s` to drop it\n", p.Name, p.Root, p.Root)
			continue
		}
		err = installClient(r, client)
		r.Close()
		if err != nil {
			fmt.Fprintf(out, "%s: client not updated: %v\n", p.Name, err)
			failed++
			continue
		}
		fmt.Fprintf(out, "%s: client updated\n", p.Name)
	}
	if failed > 0 {
		return fmt.Errorf("%d project(s) not updated, see above", failed)
	}
	return nil
}

// warnLegacyAddon lists projects that still have the old per-project add-on.
func warnLegacyAddon(reg *registry.Registry, out io.Writer) {
	for _, p := range reg.Projects {
		if _, err := os.Stat(filepath.Join(p.Root, ".ddev", "addon-metadata", "ddev-bridge")); err == nil {
			fmt.Fprintf(out, "%s still has the old ddev-bridge add-on; in %s run:\n  ddev add-on remove ddev-bridge && ddev bridge enable && ddev bridge approve\n", p.Name, p.Root)
		}
	}
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// replaceTree copies src next to dst and swaps it in with renames, so dst is
// never half-written.
func replaceTree(src, dst string) error {
	tmp, old := dst+".new", dst+".old"
	os.RemoveAll(tmp)
	os.RemoveAll(old)
	if err := copyTree(src, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	return os.RemoveAll(old)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(p, target, info.Mode().Perm())
		default:
			return fmt.Errorf("%s: not a regular file", p)
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// installFile copies src to dst via a temp file and rename.
func installFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := copyFile(src, tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// sbxAllow lets sandboxes reach the bridge.
func sbxAllow(out io.Writer) {
	sbx, err := exec.LookPath("sbx")
	if err != nil {
		fmt.Fprintln(out, "sbx not found; on the machine that runs the sandboxes, run once: sbx policy allow network localhost:7474")
		return
	}
	b, err := exec.Command(sbx, "policy", "allow", "network", "localhost:7474").CombinedOutput()
	if err != nil {
		fmt.Fprintf(out, "could not add the sbx network rule (%v): %s\nrun: sbx policy allow network localhost:7474\n", err, strings.TrimSpace(string(b)))
		return
	}
	fmt.Fprintln(out, "sbx: sandboxes may reach the bridge on localhost:7474")
}
