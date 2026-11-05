// Package rules reads, matches and edits the host policy file: allow and deny
// rules the developer keeps on the host, on top of the bridge's built-in policy.
//
// The file is plain text, one rule per line:
//
//	# comment
//	deny  drush sql:drop        # reason shown to the agent
//	allow ddev composer %
//
//	[project-name]
//	deny  ddev get_dump
//
// Rules before the first [section] apply to every project; a section applies
// to the ddev project of that name. A rule's words match the start of a
// command's words; % inside a word matches any text, and a lone % as the last
// word matches any further words, including none.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ddev-bridge/internal/registry"
)

// Rule actions.
const (
	Allow = "allow"
	Deny  = "deny"
)

// Rule is one allow or deny line of the policy file.
type Rule struct {
	Action  string
	Words   []string
	Reason  string // trailing comment, shown to the agent
	Section string // project name, "" for the global rules
	File    string
	Line    int // 1-based
}

// Pattern returns the rule's words as written.
func (r Rule) Pattern() string { return strings.Join(r.Words, " ") }

func (r Rule) String() string { return r.Action + " " + r.Pattern() }

// File is a parsed policy file. It keeps the original lines, so edits leave
// comments and layout alone.
type File struct {
	Path  string
	Rules []Rule
	lines []string
}

// DefaultPath returns the policy file location: "policy" next to the registry.
func DefaultPath() (string, error) {
	p, err := registry.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "policy"), nil
}

// Load reads the policy file at path. A missing file has no rules.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Path: path}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(path, string(data))
}

// Parse parses the policy file content. Errors name the file and line.
func Parse(path, data string) (*File, error) {
	f := &File{Path: path}
	if data != "" {
		f.lines = strings.Split(strings.TrimSuffix(data, "\n"), "\n")
	}
	section := ""
	for i, raw := range f.lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		fail := func(format string, a ...any) (*File, error) {
			return nil, fmt.Errorf("%s:%d: %s", path, i+1, fmt.Sprintf(format, a...))
		}
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "["):
			name, ok := strings.CutSuffix(line, "]")
			name = strings.TrimSpace(strings.TrimPrefix(name, "["))
			if !ok || name == "" || strings.ContainsAny(name, " \t[]") {
				return fail("section must be [project-name], got %s", line)
			}
			section = name
			continue
		}
		body, reason := splitComment(line)
		fields := strings.Fields(body)
		if fields[0] != Allow && fields[0] != Deny {
			return fail("expected allow or deny, got %q", fields[0])
		}
		if err := ValidateWords(fields[1:]); err != nil {
			return fail("%v", err)
		}
		f.Rules = append(f.Rules, Rule{Action: fields[0], Words: fields[1:], Reason: reason, Section: section, File: path, Line: i + 1})
	}
	return f, nil
}

// splitComment splits "deny ddev x  # why" into the rule and its reason.
func splitComment(line string) (string, string) {
	for i := 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
	}
	return line, ""
}

// ValidateWords checks a rule's words: a command that starts with ddev or
// drush, % as the only wildcard.
func ValidateWords(words []string) error {
	if len(words) == 0 {
		return errors.New("the rule names no command (e.g. `deny ddev get_dump`)")
	}
	if words[0] != "ddev" && words[0] != "drush" {
		return fmt.Errorf("a rule starts with ddev or drush, got %q", words[0])
	}
	for _, w := range words {
		if strings.Contains(w, "*") {
			return fmt.Errorf("%q: use %% as the wildcard, not * (the shell expands *)", w)
		}
		if strings.HasPrefix(w, "-") {
			return fmt.Errorf("%q: options are ignored when matching; leave them out of the rule", w)
		}
	}
	return nil
}

// For returns the rules that apply to project, in evaluation order: the
// global rules first, then the project's section.
func (f *File) For(project string) []Rule {
	var global, own []Rule
	for _, r := range f.Rules {
		switch r.Section {
		case "":
			global = append(global, r)
		case project:
			own = append(own, r)
		}
	}
	return append(global, own...)
}

// Match reports whether pattern matches the start of words. % inside a word
// matches any text; a lone % as the last pattern word matches any further words.
func Match(pattern, words []string) bool {
	for i, p := range pattern {
		if p == "%" && i == len(pattern)-1 {
			return true
		}
		if i >= len(words) || !matchWord(p, words[i]) {
			return false
		}
	}
	return true
}

// matchWord matches one word against a pattern where % matches any text.
func matchWord(p, w string) bool {
	parts := strings.Split(p, "%")
	if len(parts) == 1 {
		return p == w
	}
	if !strings.HasPrefix(w, parts[0]) {
		return false
	}
	w = w[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(w, part)
		if i < 0 {
			return false
		}
		w = w[i+len(part):]
	}
	return len(w) >= len(last) && strings.HasSuffix(w, last)
}

// Set adds a rule to section ("" for the global rules), or replaces the rule
// with the same words there; an empty reason keeps the replaced rule's reason. It returns the rule as stored and whether an
// existing rule was replaced. Call Save to write the file.
func (f *File) Set(section, action string, words []string, reason string) (Rule, bool, error) {
	if action != Allow && action != Deny {
		return Rule{}, false, fmt.Errorf("unknown action %q", action)
	}
	if err := ValidateWords(words); err != nil {
		return Rule{}, false, err
	}
	if strings.ContainsAny(reason, "\r\n") {
		return Rule{}, false, errors.New("the reason must be a single line")
	}
	text := action + " " + strings.Join(words, " ")
	line := func(reason string) string {
		if reason == "" {
			return text
		}
		return text + "  # " + reason
	}
	replaced := false
	at := -1 // line index to insert at
	for _, r := range f.Rules {
		if r.Section == section && r.Pattern() == strings.Join(words, " ") {
			if reason == "" {
				reason = r.Reason
			}
			f.lines[r.Line-1] = line(reason)
			replaced = true
			break
		}
	}
	text = line(reason)
	if !replaced {
		at = f.insertAt(section)
		if at < 0 { // new project section at the end
			for len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) == "" {
				f.lines = f.lines[:len(f.lines)-1]
			}
			if len(f.lines) > 0 {
				f.lines = append(f.lines, "")
			}
			f.lines = append(f.lines, "["+section+"]", text)
		} else {
			f.lines = append(f.lines[:at], append([]string{text}, f.lines[at:]...)...)
		}
	}
	if err := f.reparse(); err != nil {
		return Rule{}, false, err
	}
	for _, r := range f.Rules {
		if r.Section == section && r.Pattern() == strings.Join(words, " ") {
			return r, replaced, nil
		}
	}
	return Rule{}, false, errors.New("rule not found after adding it")
}

// insertAt returns the line index for a new rule in section: after the
// section's last rule (or its header), or -1 if the project section is missing.
func (f *File) insertAt(section string) int {
	last := -1
	for _, r := range f.Rules {
		if r.Section == section {
			last = r.Line - 1
		}
	}
	if last >= 0 {
		return last + 1
	}
	for i, l := range f.lines {
		l = strings.TrimSpace(l)
		if section == "" && strings.HasPrefix(l, "[") {
			// no global rule yet: before the first section, with a blank line in between
			j := i
			for j > 0 && strings.TrimSpace(f.lines[j-1]) == "" {
				j--
			}
			if j == i {
				f.lines = append(f.lines[:i], append([]string{""}, f.lines[i:]...)...)
			}
			return j
		}
		if section != "" && l == "["+section+"]" {
			return i + 1
		}
	}
	if section == "" {
		return len(f.lines)
	}
	return -1
}

// Remove deletes rule r (as returned by For or Rules). Call Save to write the file.
func (f *File) Remove(r Rule) error {
	i := r.Line - 1
	if i < 0 || i >= len(f.lines) {
		return fmt.Errorf("line %d is not in %s", r.Line, f.Path)
	}
	f.lines = append(f.lines[:i], f.lines[i+1:]...)
	return f.reparse()
}

func (f *File) reparse() error {
	g, err := Parse(f.Path, f.content())
	if err != nil {
		return err
	}
	f.Rules, f.lines = g.Rules, g.lines
	return nil
}

func (f *File) content() string {
	if len(f.lines) == 0 {
		return ""
	}
	return strings.Join(f.lines, "\n") + "\n"
}

// Save writes the file atomically with 0600 permissions.
func (f *File) Save() error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(f.content()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}
