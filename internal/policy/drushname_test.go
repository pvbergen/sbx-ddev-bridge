package policy

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDrushList(t *testing.T) {
	out := []byte("Some ddev notice\n" + `{"commands":[
		{"name":"cache:rebuild","usage":["cache:rebuild [options]","drush cr --help","cr","rebuild"]},
		{"name":"cr2","usage":["cr2"]},
		{"name":"rebuild","usage":["rebuild"]}]}`)
	c, err := ParseDrushList(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"cache:rebuild": "cache:rebuild", "cr": "cache:rebuild", "cr2": "cr2", "rebuild": "rebuild"}
	if len(c.names) != len(want) {
		t.Fatalf("names = %v", c.names)
	}
	for k, v := range want { // a command name is never taken over by another command's alias
		if c.names[k] != v {
			t.Fatalf("names[%q] = %q, want %q (all: %v)", k, c.names[k], v, c.names)
		}
	}
	for _, bad := range []string{"", "not json", `{"commands":[]}`} {
		if _, err := ParseDrushList([]byte(bad)); err == nil {
			t.Fatalf("ParseDrushList(%q) succeeded", bad)
		}
	}
}

func TestResolveDrush(t *testing.T) {
	cmds, _ := fixtureDrush(t)(false)
	cases := []struct{ name, want, err string }{
		{"cr", "cache:rebuild", ""},
		{"cache:rebuild", "cache:rebuild", ""},
		{"cache:reb", "cache:rebuild", ""},
		{"c:reb", "cache:rebuild", ""},
		{"CACHE:REBUILD", "cache:rebuild", ""},
		{"php:ev", "php:eval", ""},
		{"core:cl", "php:cli", ""}, // via the alias core:cli
		{"pm:en", "pm:install", ""},
		{"some:contrib", "some:contrib-cmd", ""}, // hidden commands are listed too
		{"sql:s", "", "ambiguous"},
		{"sql", "", "ambiguous"},       // sqlq, sqlsan, sql:… as in Symfony
		{"some", "", "unknown"},        // only a namespace
		{"cache", "cache:rebuild", ""}, // via the alias cache-rebuild, as in Symfony
		{"nosuch", "", "unknown"},
		{"cache:rebuild:x", "", "unknown"},
		{"(.*)", "", "unknown"}, // regexp metacharacters are literal
	}
	for _, c := range cases {
		got, err := cmds.Resolve(c.name)
		if got != c.want || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("Resolve(%q) = %q, %v; want %q, %s", c.name, got, err, c.want, c.err)
		}
	}
}

func TestResolveDrushRefreshesOnce(t *testing.T) {
	old, _ := ParseDrushList([]byte(`{"commands":[{"name":"cache:rebuild","usage":["cr"]}]}`))
	updated, _ := ParseDrushList([]byte(`{"commands":[{"name":"cache:rebuild","usage":["cr"]},{"name":"new:cmd","usage":[]}]}`))
	var calls []bool
	p := Project{Drush: func(refresh bool) (*DrushCommands, error) {
		calls = append(calls, refresh)
		if refresh {
			return updated, nil
		}
		return old, nil
	}}

	if got, d := resolveDrush(p, "cr"); !d.Allowed || got != "cache:rebuild" || len(calls) != 1 {
		t.Fatalf("known name: %q %+v calls=%v", got, d, calls)
	}
	calls = nil
	if got, d := resolveDrush(p, "new:cmd"); !d.Allowed || got != "new:cmd" || len(calls) != 2 || !calls[1] {
		t.Fatalf("new command: %q %+v calls=%v", got, d, calls)
	}
	calls = nil
	if _, d := resolveDrush(p, "nosuch"); d.Allowed || len(calls) != 2 {
		t.Fatalf("unknown: %+v calls=%v", d, calls)
	}

	failing := Project{Drush: func(bool) (*DrushCommands, error) { return nil, errors.New("project is not running") }}
	if _, d := resolveDrush(failing, "cr"); d.Allowed || !strings.Contains(d.Reason, "project is not running") {
		t.Fatalf("lister error: %+v", d)
	}
	if _, d := resolveDrush(Project{}, "cr"); d.Allowed {
		t.Fatal("no lister must deny")
	}
	if d := Check(Project{}, []string{"drush"}); !d.Allowed {
		t.Fatalf("bare drush needs no list: %+v", d)
	}
}
