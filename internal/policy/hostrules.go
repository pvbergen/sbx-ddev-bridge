package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ddev-bridge/internal/rules"
)

// Aliases of denied ddev subcommands, so host rules see one name.
var ddevDeniedAliases = map[string]string{"ls": "list", "powerdown": "poweroff"}

// Static drush aliases of denied commands (ev -> php:eval), used when the
// project's drush list is not available yet.
var drushDeniedAliases = aliasTable(drushDeniedGroups)

// applyHostRules lets the last matching host rule decide over the built-in
// decision d. Calls that can't be normalized keep the built-in decision.
func applyHostRules(p Project, argv []string, d Decision) Decision {
	if len(p.Rules) == 0 {
		return d
	}
	words, run, err := commandWords(p, argv)
	if err != nil {
		return d
	}
	var match *rules.Rule
	for i := range p.Rules {
		if rules.Match(ruleWords(p, p.Rules[i].Words), words) {
			match = &p.Rules[i]
		}
	}
	switch {
	case match == nil:
		return d
	case match.Action == rules.Deny:
		why := "ask the developer if you need it"
		if match.Reason != "" {
			why = match.Reason
		}
		return Decision{Reason: fmt.Sprintf("denied by the developer's host policy (%s:%d: %s): %s", match.File, match.Line, match, why), Rule: match}
	case d.Allowed:
		d.Rule = match
		return d
	default:
		return Decision{Allowed: true, Argv: run, Rule: match, Override: d.Reason}
	}
}

// commandWords returns the call as host rules see it, plus the argv to run if
// a rule allows a call the built-in policy denies. Every route to a command
// yields the same words: `drush <name> …` for drush however it is called,
// `ddev composer …` for composer, `ddev exec <program> …`, else
// `ddev <subcommand> …`. Options are left out.
func commandWords(p Project, argv []string) (words, run []string, err error) {
	cmd, args := argv[0], argv[1:]
	canonical := cmd
	if c, ok := ddevAllowed[cmd]; ok {
		canonical = c
	} else if c, ok := ddevDeniedAliases[cmd]; ok {
		canonical = c
	}
	switch canonical {
	case "drush":
		words, err = drushWords(p, args)
		return words, argv, err
	case "exec":
		flags, c, d := parseExec(argv)
		if !d.Allowed {
			return nil, nil, errors.New(d.Reason)
		}
		words, err = toolWords(p, c[0], c[1:], true)
		return words, execArgv(flags, c), err
	case "composer", "php", "mysql", "mariadb", "psql", "npm", "yarn":
		words, err = toolWords(p, canonical, args, false)
		return words, argv, err
	}
	return append([]string{"ddev", canonical}, positionals(args)...), argv, nil
}

func toolWords(p Project, bin string, args []string, viaExec bool) ([]string, error) {
	name := base(bin)
	if interpreter(name) == "php" {
		if i := firstPositional(args); i >= 0 {
			switch base(args[i]) {
			case "drush", "drush.php":
				return drushWords(p, args[i+1:])
			case "composer", "composer.phar":
				return append([]string{"ddev", "composer"}, positionals(args[i+1:])...), nil
			}
		}
	}
	switch name {
	case "drush", "drush.php":
		return drushWords(p, args)
	case "composer", "composer.phar":
		return append([]string{"ddev", "composer"}, positionals(args)...), nil
	}
	if viaExec {
		return append([]string{"ddev", "exec", name}, positionals(args)...), nil
	}
	return append([]string{"ddev", name}, positionals(args)...), nil
}

// drushWords returns `drush [@alias] <canonical command> args…`.
func drushWords(p Project, args []string) ([]string, error) {
	out := []string{"drush"}
	command := false
	for _, a := range positionals(args) {
		switch {
		case strings.HasPrefix(a, "@"):
			if a != "@self" && a != "@none" {
				out = append(out, a)
			}
		case !command:
			command = true
			c, d := resolveDrush(p, a)
			if !d.Allowed {
				return nil, errors.New(d.Reason)
			}
			if c == a { // not listed yet: at least map the static aliases
				if s, ok := drushDeniedAliases[strings.ToLower(a)]; ok {
					c = s
				}
			}
			out = append(out, c)
		default:
			out = append(out, a)
		}
	}
	return out, nil
}

// positionals returns the arguments that are not options.
func positionals(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--" {
			return append(out, args[i+1:]...)
		}
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// ruleWords normalizes a rule like a command, so `deny drush cim` and
// `deny ddev drush cim` match `drush config:import`.
func ruleWords(p Project, w []string) []string {
	if len(w) >= 2 && w[0] == "ddev" {
		switch {
		case w[1] == "drush":
			w = append([]string{"drush"}, w[2:]...)
		case w[1] == "exec" && len(w) >= 3 && (w[2] == "drush" || w[2] == "drush.php"):
			w = append([]string{"drush"}, w[3:]...)
		case w[1] == "exec" && len(w) >= 3 && (w[2] == "composer" || w[2] == "composer.phar"):
			w = append([]string{"ddev", "composer"}, w[3:]...)
		default:
			c := w[1]
			if a, ok := ddevAllowed[c]; ok {
				c = a
			} else if a, ok := ddevDeniedAliases[c]; ok {
				c = a
			}
			w = append([]string{"ddev", c}, w[2:]...)
		}
	}
	if len(w) >= 2 && w[0] == "drush" && !strings.Contains(w[1], "%") && !strings.HasPrefix(w[1], "@") {
		name := w[1]
		if p.Drush != nil {
			if cmds, err := p.Drush(false); err == nil {
				if c, err := cmds.Resolve(name); err == nil {
					name = c
				}
			}
		}
		if name == w[1] {
			if s, ok := drushDeniedAliases[strings.ToLower(name)]; ok {
				name = s
			}
		}
		w = append([]string{"drush", name}, w[2:]...)
	}
	return w
}

// LiftsBuiltin returns examples of built-in denials that an allow rule
// overrides, so the developer sees what a broad allow opens.
func LiftsBuiltin(r rules.Rule) []string {
	if r.Action != rules.Allow {
		return nil
	}
	pattern := ruleWords(Project{}, r.Words)
	var out []string
	for _, probe := range builtinProbes() {
		if rules.Match(pattern, probe) {
			out = append(out, strings.Join(probe, " "))
		}
	}
	return out
}

// builtinProbes are representative calls the built-in policy denies.
func builtinProbes() [][]string {
	var out [][]string
	var ddev []string
	for c := range ddevDenied {
		if c != "bridge" && ddevDeniedAliases[c] == "" {
			ddev = append(ddev, c)
		}
	}
	sort.Strings(ddev)
	for _, c := range ddev {
		out = append(out, []string{"ddev", c})
	}
	out = append(out,
		[]string{"ddev", "add-on", "get"}, []string{"ddev", "add-on", "remove"}, []string{"ddev", "config", "global"},
		[]string{"ddev", "composer", "exec"}, []string{"ddev", "composer", "run-script"}, []string{"ddev", "composer", "global"},
		[]string{"drush", "@remote"})
	var drush []string
	for c := range drushDeniedGroups {
		drush = append(drush, c)
	}
	sort.Strings(drush)
	for _, c := range drush {
		out = append(out, []string{"drush", c})
	}
	for _, set := range []map[string]bool{shells, wrappers, netTools} {
		var names []string
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, []string{"ddev", "exec", n})
		}
	}
	return append(out, []string{"ddev", "exec", "npx"})
}
