// Package policy decides which ddev invocations the bridge will run.
//
// Policy v2: everything that stays inside the registered project is allowed.
// The bridge denies what would make ddev reach the web (remote environments,
// tunnels, network tools), touch the host or other projects, or open a shell /
// evaluate inline code (which would trivially bypass the other rules).
//
// These are argv checks, not a network boundary: code written to disk and run
// later (composer scripts, test bootstraps, scripts) is not inspected.
//
// Check may rewrite argv (e.g. force `ddev exec --raw`) so that what runs is
// exactly what was checked.
package policy

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Project is the context a request is checked against. It is filled in by the
// server from the registry and the host, never from the request.
type Project struct {
	Name           string      // ddev project name of the registered project
	Root           string      // absolute host path of the project
	OtherProjects  []string    // names of other ddev projects on this host
	CustomCommands []string    // custom ddev commands available to this project (project + global)
	Drush          DrushLister // the project's drush commands, to resolve aliases and abbreviations
}

// Decision is the result of checking an argv.
type Decision struct {
	Allowed bool
	Reason  string   // set when denied
	Argv    []string // argv to execute when allowed (may differ from the input)
}

func allow(argv []string) Decision { return Decision{Allowed: true, Argv: argv} }

func deny(format string, a ...any) Decision { return Decision{Reason: fmt.Sprintf(format, a...)} }

// Reason suffixes, so the agent can tell the categories apart.
const (
	whyWeb   = "it reaches the web; the bridge only allows local work"
	whyHost  = "it affects the host or other projects; the bridge only acts on this project"
	whyShell = "shells and inline code evaluation are not allowed through the bridge"
)

var safeValue = regexp.MustCompile(`^[A-Za-z0-9_.:,/-]{1,256}$`)

// aliasTable maps every accepted name (canonical and aliases) to the canonical name.
func aliasTable(groups map[string][]string) map[string]string {
	m := map[string]string{}
	for canonical, aliases := range groups {
		m[canonical] = canonical
		for _, a := range aliases {
			m[a] = canonical
		}
	}
	return m
}

// splitOpt splits "--name=value" into ("--name", "value", true).
func splitOpt(a string) (string, string, bool) {
	if i := strings.IndexByte(a, '='); i > 0 && strings.HasPrefix(a, "-") {
		return a[:i], a[i+1:], true
	}
	return a, "", false
}

// firstPositional returns the index of the first argument that is not an option.
func firstPositional(args []string) int {
	for i, a := range args {
		if a == "--" {
			if i+1 < len(args) {
				return i + 1
			}
			return -1
		}
		if !strings.HasPrefix(a, "-") {
			return i
		}
	}
	return -1
}

// hasFlag reports whether args contain one of the flags, bare or as --flag=value.
// Single-letter flags also match their attached-value form (-hfoo).
func hasFlag(args []string, flags ...string) (string, bool) {
	for _, a := range args {
		if a == "--" {
			return "", false
		}
		name, _, _ := splitOpt(a)
		for _, f := range flags {
			if name == f {
				return f, true
			}
			if len(f) == 2 && f[0] == '-' && f[1] != '-' && strings.HasPrefix(a, f) && !strings.HasPrefix(a, "--") {
				return f, true
			}
		}
	}
	return "", false
}

// insideRoot reports whether host path p (relative paths are taken relative to
// root) stays inside root, following symlinks of the existing part of the path.
func insideRoot(root, p string) bool {
	if root == "" {
		return false
	}
	if strings.HasPrefix(p, "~") {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	resolved := resolveExisting(p)
	rootResolved := resolveExisting(filepath.Clean(root))
	for _, pair := range [][2]string{{rootResolved, resolved}, {filepath.Clean(root), p}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// resolveExisting resolves symlinks in the longest existing prefix of p.
func resolveExisting(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// looksLikeHostPath reports whether v names a host path that could leave the project.
func looksLikeHostPath(v string) bool {
	return strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") || v == ".." ||
		strings.HasPrefix(v, "../") || strings.Contains(v, "/../") || strings.HasSuffix(v, "/..")
}

// base returns the program name of a (possibly path-qualified) binary.
func base(bin string) string { return path.Base(bin) }

// Check evaluates argv (without the leading "ddev") for project p.
func Check(p Project, argv []string) Decision {
	if len(argv) == 0 {
		return deny("empty command")
	}
	for _, a := range argv {
		if strings.ContainsRune(a, 0) {
			return deny("NUL bytes in arguments are not allowed")
		}
	}
	return checkDdev(p, argv)
}
