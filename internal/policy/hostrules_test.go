package policy

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"ddev-bridge/internal/rules"
)

func withRules(t *testing.T, text string) Project {
	t.Helper()
	f, err := rules.Parse("policy", text)
	if err != nil {
		t.Fatal(err)
	}
	p := testProject(t)
	p.Rules = f.For(p.Name)
	return p
}

func TestCommandWords(t *testing.T) {
	p := testProject(t)
	for argv, want := range map[string]string{
		"drush cim -y":                         "drush config:import",
		"drush @self --uri=x sql:q select":     "drush sql:query select",
		"drush @stage st":                      "drush @stage core:status",
		"exec drush php:ev x":                  "drush php:eval x",
		"exec -s web -- vendor/bin/drush cr":   "drush cache:rebuild",
		"php vendor/bin/drush.php sql:drop -y": "drush sql:drop",
		"exec php composer.phar require a/b":   "ddev composer require a/b",
		"composer -n require a/b":              "ddev composer require a/b",
		"exec composer.phar update":            "ddev composer update",
		"exec vendor/bin/phpunit --filter=X":   "ddev exec phpunit",
		"exec --raw -- curl example.com":       "ddev exec curl example.com",
		"ut test":                              "ddev utility test",
		"ls":                                   "ddev list",
		"get_dump stage":                       "ddev get_dump stage",
		"import-db --file=dump.sql.gz":         "ddev import-db",
		"stop otherproj":                       "ddev stop otherproj",
		"mysql -e x":                           "ddev mysql x", // a separate option value counts as a word
	} {
		words, _, err := commandWords(p, strings.Fields(argv))
		if err != nil || strings.Join(words, " ") != want {
			t.Errorf("%s: words = %q, %v; want %q", argv, words, err, want)
		}
	}
	// Calls that can't be normalized keep the built-in decision.
	for _, argv := range []string{"drush nosuch:cmd", "drush -l x php:eval", "exec", "exec --bogus ls"} {
		if _, _, err := commandWords(p, strings.Fields(argv)); err == nil {
			t.Errorf("%s: normalized", argv)
		}
	}
}

func TestHostRules(t *testing.T) {
	p := withRules(t, `
deny  drush sql:drop        # ask first
deny  ddev %
allow ddev composer %
allow ddev describe
allow drush %
deny  drush user:%
allow ddev exec phpunit
[demo]
deny  ddev mycmd            # ssh to live
allow drush php:eval
deny  drush @stage %
[otherproject]
allow ddev mycmd
`)
	cases := []struct {
		argv  string
		allow bool
		rule  string // matching rule
	}{
		{"drush sql:drop -y", true, "allow drush %"}, // later allow wins over the earlier deny
		{"drush uli", false, "deny drush user:%"},    // alias of user:login
		{"composer require a/b", true, "allow ddev composer %"},
		{"st", true, "allow ddev describe"},
		{"restart", false, "deny ddev %"},
		{"mycmd", false, "deny ddev mycmd"}, // custom commands: project section
		{"exec vendor/bin/phpunit", true, "allow ddev exec phpunit"},
		{"exec php vendor/bin/drush ev 1", true, "allow drush php:eval"}, // overrides the built-in denial
		{"drush @stage st", false, "deny drush @stage %"},
		{"drush @live st", true, "allow drush %"}, // overrides the built-in alias denial
		{"bridge approve", false, ""},             // never overridable
		{"exec bash", false, "deny ddev %"},
	}
	for _, c := range cases {
		d := Check(p, strings.Fields(c.argv))
		rule := ""
		if d.Rule != nil {
			rule = d.Rule.String()
		}
		if d.Allowed != c.allow || rule != c.rule {
			t.Errorf("%s: allowed=%v rule=%q (%s); want %v %q", c.argv, d.Allowed, rule, d.Reason, c.allow, c.rule)
		}
	}
	d := Check(p, strings.Fields("mycmd"))
	if !strings.Contains(d.Reason, "policy:10: deny ddev mycmd): ssh to live") {
		t.Errorf("reason = %q", d.Reason)
	}
}

func TestHostAllowOverrides(t *testing.T) {
	p := withRules(t, "allow ddev exec curl\nallow drush php:eval\nallow ddev pull\n")
	d := Check(p, strings.Fields("exec curl -s x"))
	if !d.Allowed || d.Override == "" || !reflect.DeepEqual(d.Argv, strings.Fields("exec --raw -- curl -s x")) {
		t.Errorf("exec curl: %+v", d)
	}
	d = Check(p, strings.Fields("drush php:ev 1"))
	if !d.Allowed || !strings.Contains(d.Override, "php:eval") || !reflect.DeepEqual(d.Argv, strings.Fields("drush php:ev 1")) {
		t.Errorf("php:ev: %+v", d)
	}
	if d := Check(p, strings.Fields("drush cr")); !d.Allowed || d.Override != "" || d.Rule != nil {
		t.Errorf("no rule: %+v", d)
	}
	// Static aliases work before the project's drush list is known.
	p.Drush = func(bool) (*DrushCommands, error) { return nil, ErrDrushNotListed }
	if d := Check(p, strings.Fields("drush ev 1")); !d.Allowed {
		t.Errorf("ev unlisted: %+v", d)
	}
	if d := Check(p, strings.Fields("pull x")); !d.Allowed || d.Override == "" {
		t.Errorf("pull: %+v", d)
	}
}

func TestRuleWordsAreNormalized(t *testing.T) {
	p := withRules(t, "deny ddev drush cim\ndeny ddev exec composer require\ndeny ddev ut test\n")
	for _, argv := range []string{"drush config:import", "exec drush cim", "composer require x", "utility test"} {
		if d := Check(p, strings.Fields(argv)); d.Allowed || d.Rule == nil {
			t.Errorf("%s: %+v", argv, d)
		}
	}
}

func TestInvalidPolicyDeniesEverything(t *testing.T) {
	p := testProject(t)
	p.RulesErr = errors.New("policy:3: expected allow or deny")
	if d := Check(p, strings.Fields("drush cr")); d.Allowed || !strings.Contains(d.Reason, "policy:3") {
		t.Errorf("%+v", d)
	}
}

func TestLiftsBuiltin(t *testing.T) {
	for rule, want := range map[string]string{
		"allow drush %":                 "drush @remote",
		"allow drush php:%":             "drush php:eval",
		"allow ddev exec %":             "ddev exec bash",
		"allow ddev %":                  "ddev pull",
		"allow ddev drush ev":           "drush php:eval",
		"allow drush cache:%":           "",
		"deny drush %":                  "",
		"allow ddev composer require %": "",
	} {
		f, err := rules.Parse("p", rule)
		if err != nil {
			t.Fatal(err)
		}
		got := LiftsBuiltin(f.Rules[0])
		if (want == "") != (len(got) == 0) || want != "" && !strings.Contains(strings.Join(got, ","), want) {
			t.Errorf("%s: %v, want %q", rule, got, want)
		}
	}
}
