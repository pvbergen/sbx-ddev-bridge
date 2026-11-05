package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"ddev-bridge/internal/policy"
	"ddev-bridge/internal/registry"
	"ddev-bridge/internal/rules"
)

const policyUsage = `usage:
  ddev-bridge policy ls [--dir DIR]
  ddev-bridge policy allow|deny [--dir DIR] [--global] [--reason TEXT] <words…>
  ddev-bridge policy rm [--dir DIR] [N]
  ddev-bridge policy reset [--dir DIR] [--global] [--yes]

Rules match the start of a command: "drush sql:drop", "ddev get_dump", "ddev exec phpunit".
% matches any text inside a word ("drush sql:%"); a lone % at the end matches any further
words ("drush %"). Don't use *, the shell expands it. The last matching rule wins, also over
the bridge's built-in denials. Without --global a rule applies to the project in DIR only.
reset removes all rules of the project in DIR (its whole section), or with --global all global rules.
`

// policyCmd runs `ddev-bridge policy …` and returns the exit code.
func policyCmd(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("policy "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", ".", "ddev project directory")
	global := fs.Bool("global", false, "add the rule for every project")
	reason := fs.String("reason", "", "reason shown to the agent when the rule denies")
	yes := fs.Bool("yes", false, "reset without asking")
	maxPos := map[string]int{"ls": 0, "rm": 1, "reset": 0, "allow": 1 << 20, "deny": 1 << 20}
	n, known := maxPos[sub]
	if !known {
		fmt.Fprintf(os.Stderr, "ddev-bridge policy: unknown subcommand %q\n%s", sub, policyUsage)
		return 2
	}
	pos, err := parseFlags(fs, args, n)
	addRule := sub == "allow" || sub == "deny"
	switch {
	case err != nil:
	case *global && !addRule && sub != "reset":
		err = fmt.Errorf("--global only applies to allow, deny and reset")
	case *reason != "" && !addRule:
		err = fmt.Errorf("--reason only applies to allow and deny")
	case *yes && sub != "reset":
		err = fmt.Errorf("--yes only applies to reset")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ddev-bridge policy %s: %v\n%s", sub, err, policyUsage)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(os.Stderr, "ddev-bridge policy %s: %v\n", sub, err)
		return 1
	}

	path, err := rules.DefaultPath()
	if err != nil {
		return fail(err)
	}
	f, err := rules.Load(path)
	if err != nil {
		return fail(fmt.Errorf("%v; fix the file by hand", err))
	}
	project := ""
	if !(*global && sub != "ls") {
		if _, project, err = registry.ValidateRoot(*dir); err != nil {
			return fail(err)
		}
	}

	switch sub {
	case "ls":
		listRules(stdout, f, project)
	case "allow", "deny":
		// A single quoted argument ("drush sql:%") is split into words too.
		words := strings.Fields(strings.Join(pos, " "))
		section := project
		if *global {
			section = ""
		}
		r, replaced, err := f.Set(section, sub, words, *reason)
		if err != nil {
			return fail(err)
		}
		if err := f.Save(); err != nil {
			return fail(err)
		}
		verb := "added"
		if replaced {
			verb = "replaced"
		}
		fmt.Fprintf(stdout, "%s %q in %s (%s:%d)\n", verb, r.String(), sectionName(r.Section), r.File, r.Line)
		if lifted := policy.LiftsBuiltin(r); len(lifted) > 0 {
			fmt.Fprintf(stdout, "note: this rule overrides built-in denials, e.g. %s\n", examples(lifted))
		}
	case "rm":
		list := f.For(project)
		if len(list) == 0 {
			fmt.Fprintf(stdout, "no rules for %s in %s\n", project, path)
			return 0
		}
		choice := ""
		if len(pos) == 1 {
			choice = pos[0]
		} else {
			if !isTerminal(stdin) {
				return fail(fmt.Errorf("give the rule number from `policy ls`"))
			}
			listRules(stdout, f, project)
			fmt.Fprintf(stdout, "Remove which rule? [1-%d, Enter cancels] ", len(list))
			line, _ := bufio.NewReader(stdin).ReadString('\n')
			if choice = strings.TrimSpace(line); choice == "" {
				fmt.Fprintln(stdout, "nothing removed")
				return 0
			}
		}
		i, err := strconv.Atoi(choice)
		if err != nil || i < 1 || i > len(list) {
			return fail(fmt.Errorf("%q is not a rule number between 1 and %d", choice, len(list)))
		}
		r := list[i-1]
		if err := f.Remove(r); err != nil {
			return fail(err)
		}
		if err := f.Save(); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "removed %q from %s (%s:%d)\n", r.String(), sectionName(r.Section), r.File, r.Line)
	case "reset":
		section := project
		if *global {
			section = ""
		}
		var list []rules.Rule
		for _, r := range f.Rules {
			if r.Section == section {
				list = append(list, r)
			}
		}
		if len(list) == 0 {
			fmt.Fprintf(stdout, "no rules in %s of %s\n", sectionName(section), path)
			return 0
		}
		if !*yes {
			if !isTerminal(stdin) {
				return fail(fmt.Errorf("pass --yes to reset without a terminal"))
			}
			fmt.Fprintf(stdout, "%s in %s:\n", sectionName(section), path)
			for _, r := range list {
				fmt.Fprintf(stdout, "  %s\n", r.String())
			}
			fmt.Fprintf(stdout, "Remove these %d rules? [y/N] ", len(list))
			line, _ := bufio.NewReader(stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Fprintln(stdout, "nothing removed")
				return 0
			}
		}
		removed, err := f.Reset(section)
		if err != nil {
			return fail(err)
		}
		if err := f.Save(); err != nil {
			return fail(err)
		}
		noun := "rules"
		if len(removed) == 1 {
			noun = "rule"
		}
		fmt.Fprintf(stdout, "removed %d %s from %s (%s)\n", len(removed), noun, sectionName(section), path)
	}
	return 0
}

// listRules prints the rules for project, numbered in evaluation order.
func listRules(w io.Writer, f *rules.File, project string) {
	fmt.Fprintf(w, "host policy: %s\n", f.Path)
	list := f.For(project)
	if len(list) == 0 {
		fmt.Fprintf(w, "no rules for %s; add one with `ddev bridge policy deny <words…>`\n", project)
		return
	}
	section := "-"
	for i, r := range list {
		if r.Section != section {
			section = r.Section
			fmt.Fprintln(w, sectionName(section))
		}
		line := fmt.Sprintf("%3d  %-5s %s", i+1, r.Action, r.Pattern())
		if r.Reason != "" {
			line += "  # " + r.Reason
		}
		fmt.Fprintf(w, "%s  (line %d)\n", line, r.Line)
		if lifted := policy.LiftsBuiltin(r); len(lifted) > 0 {
			fmt.Fprintf(w, "       overrides built-in denials, e.g. %s\n", examples(lifted))
		}
	}
}

// policySummary is the status line for the host policy file.
func policySummary(w io.Writer) {
	path, err := rules.DefaultPath()
	if err != nil {
		return
	}
	f, err := rules.Load(path)
	switch {
	case err != nil:
		fmt.Fprintf(w, "host policy: INVALID, the bridge denies everything: %v\n", err)
		return
	case len(f.Rules) == 0:
		fmt.Fprintf(w, "host policy: %s (no rules)\n", path)
		return
	}
	fmt.Fprintf(w, "host policy: %s (%d rules)\n", path, len(f.Rules))
	for _, r := range f.Rules {
		if lifted := policy.LiftsBuiltin(r); len(lifted) > 0 {
			fmt.Fprintf(w, "  line %d, %s: %q overrides built-in denials, e.g. %s\n", r.Line, sectionName(r.Section), r.String(), examples(lifted))
		}
	}
}

func sectionName(s string) string {
	if s == "" {
		return "global"
	}
	return "[" + s + "]"
}

func examples(xs []string) string {
	if len(xs) > 3 {
		return strings.Join(xs[:3], ", ") + fmt.Sprintf(" (+%d more)", len(xs)-3)
	}
	return strings.Join(xs, ", ")
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
