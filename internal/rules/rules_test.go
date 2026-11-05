package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# host policy
deny  drush sql:drop   # ask the developer
allow ddev composer %

[orell-fuessli]
deny ddev get_dump	# ssh to live
[other]
allow drush %
`

func TestParse(t *testing.T) {
	f, err := Parse("policy", sample)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range f.Rules {
		got = append(got, r.Section+"|"+r.String()+"|"+r.Reason)
	}
	want := []string{
		"|deny drush sql:drop|ask the developer",
		"|allow ddev composer %|",
		"orell-fuessli|deny ddev get_dump|ssh to live",
		"other|allow drush %|",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rules:\n%s", strings.Join(got, "\n"))
	}
	if f.Rules[2].Line != 6 {
		t.Errorf("line = %d, want 6", f.Rules[2].Line)
	}
	var order []string
	for _, r := range f.For("orell-fuessli") {
		order = append(order, r.Pattern())
	}
	if strings.Join(order, ",") != "drush sql:drop,ddev composer %,ddev get_dump" {
		t.Errorf("For = %v", order)
	}
}

func TestParseErrors(t *testing.T) {
	for in, want := range map[string]string{
		"permit ddev x":    `policy:1: expected allow or deny, got "permit"`,
		"deny":             "policy:1: the rule names no command",
		"deny composer x":  `a rule starts with ddev or drush, got "composer"`,
		"\nallow drush *":  `policy:2: "*": use % as the wildcard`,
		"deny drush cr -y": `"-y": options are ignored`,
		"[a b]":            "section must be [project-name]",
		"[x":               "section must be [project-name]",
	} {
		_, err := Parse("policy", in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", in, err, want)
		}
	}
}

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, words string
		want           bool
	}{
		{"drush sql:drop", "drush sql:drop", true},
		{"drush sql:drop", "drush sql:drop extra", true}, // prefix
		{"drush sql:drop", "drush sql:dump", false},
		{"drush sql:drop", "drush", false},
		{"drush %", "drush", true}, // trailing % matches no words too
		{"drush %", "drush php:eval x", true},
		{"drush sql:%", "drush sql:query", true},
		{"drush sql:%", "drush sql", false},
		{"drush %:eval", "drush php:eval", true},
		{"ddev % x", "ddev exec x", true}, // lone % in the middle: one word
		{"ddev % x", "ddev exec", false},
		{"ddev a%b%c", "ddev abxbc", true},
		{"ddev a%a", "ddev a", false},
		{"ddev", "drush cr", false},
	} {
		if got := Match(strings.Fields(c.pattern), strings.Fields(c.words)); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.pattern, c.words, got)
		}
	}
}

func TestSetAndRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "policy")
	f, err := Load(path) // missing file
	if err != nil || len(f.Rules) != 0 {
		t.Fatal(f, err)
	}
	steps := []struct {
		section, action, words, reason string
	}{
		{"proj", Deny, "ddev get_dump", "ssh to live"},
		{"", Deny, "drush sql:drop", ""},
		{"", Allow, "ddev composer %", ""},
		{"proj", Allow, "drush %", ""},
		{"proj", Allow, "ddev get_dump", ""}, // replaces in place, keeps the reason
	}
	for _, s := range steps {
		if _, _, err := f.Set(s.section, s.action, strings.Fields(s.words), s.reason); err != nil {
			t.Fatal(err)
		}
	}
	want := "deny drush sql:drop\nallow ddev composer %\n\n[proj]\nallow ddev get_dump  # ssh to live\nallow drush %\n"
	if f.content() != want {
		t.Fatalf("content:\n%s", f.content())
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode())
	}
	f, _ = Load(path)
	if err := f.Remove(f.For("proj")[0]); err != nil {
		t.Fatal(err)
	}
	if f.content() != "allow ddev composer %\n\n[proj]\nallow ddev get_dump  # ssh to live\nallow drush %\n" {
		t.Fatalf("after remove:\n%s", f.content())
	}
	if _, _, err := f.Set("", Allow, []string{"drush", "*"}, ""); err == nil {
		t.Error("* accepted")
	}
	if _, _, err := f.Set("", Deny, []string{"ddev", "x"}, "a\nb"); err == nil {
		t.Error("multi-line reason accepted")
	}
}

func TestSetKeepsCommentsAndAddsGlobalBeforeSections(t *testing.T) {
	f, _ := Parse("p", "# header\n\n[proj]\ndeny ddev x  # why\n")
	if _, _, err := f.Set("", Deny, []string{"drush", "sql:drop"}, "careful"); err != nil {
		t.Fatal(err)
	}
	want := "# header\ndeny drush sql:drop  # careful\n\n[proj]\ndeny ddev x  # why\n"
	if f.content() != want {
		t.Fatalf("content:\n%q", f.content())
	}
}
