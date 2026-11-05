package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testProject(t *testing.T) Project {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".ddev"), 0o755)
	os.WriteFile(filepath.Join(root, "dump.sql.gz"), nil, 0o644)
	// A symlink inside the project that points outside of it.
	os.Symlink("/etc", filepath.Join(root, "escape"))
	return Project{
		Name:           "demo",
		Root:           root,
		OtherProjects:  []string{"otherproj"},
		CustomCommands: []string{"mycmd", "bridge"}, // bridge is installed as a global host command
	}
}

func TestCheck(t *testing.T) {
	p := testProject(t)
	cases := []struct {
		argv  string
		allow bool
	}{
		// drush: everything local, including contrib commands.
		{"drush", true},
		{"drush cr", true},
		{"drush -y cache:rebuild", true},
		{"drush @self cr", true},
		{"drush pm:install foo -y", true},
		{"drush some:contrib-cmd --opt=1", true},
		{"drush site:install -y", true},
		{"drush sql:drop -y", true},
		{"drush sqlq SELECT 1", true},
		{"drush cex -y", true},
		{"drush uli", true},
		{"drush cron", true},

		{"drush @prod st", false},
		{"drush cr @live", false},
		{"drush sql:sync @prod @self", false},
		{"drush sql-sync @prod @self", false},
		{"drush rsync @prod:%files @self:%files", false},
		{"drush ssh", false},
		{"drush pm:security", false},
		{"drush sec", false},
		{"drush locale:update", false},
		{"drush self-update", false},
		{"drush ev echo(1);", false},
		{"drush php:eval 1", false},
		{"drush PHP:EVAL 1", false},
		{"drush scr x.php", false},
		{"drush php", false},
		{"drush cr --root=/", false},
		{"drush cr --include=/tmp/evil", false},
		{"drush -D foo=bar cr", false},
		{"drush -c=x.yml cr", false},
		{"drush cr -c x.yml", false},
		{`drush sqlq \! curl x`, false},
		{"drush sqlq select 1; system curl x", false},

		// composer: everything, including downloads, except arbitrary code / global.
		{"composer install", true},
		{"composer require drupal/x", true},
		{"composer -n update drupal/core --with-all-dependencies", true},
		{"composer outdated", true},
		{"composer create-project drupal/recommended-project x", true},
		{"composer", true},
		{"composer exec evil", false},
		{"composer run-script foo", false},
		{"composer run foo", false},
		{"composer global require x", false},
		{"composer self-update", false},
		{"composer config -g repo.x vcs", false},

		// exec: any program, except shells, wrappers, network tools, eval.
		{"exec vendor/bin/phpunit", true},
		{"exec phpunit --filter testFoo", true},
		{"exec -s db mysqldump db", true},
		{"exec git status", true},
		{"exec git diff HEAD", true},
		{"exec php vendor/bin/drush cr", true},
		{"exec ls -la web", true},
		{"exec npm install", true},
		{"exec -d /var/www/html vendor/bin/phpcs --standard=Drupal web/modules/custom", true},
		{". drush cr", true},

		{"exec bash", false},
		{"exec bash -c curl x", false},
		{"exec /bin/sh -c id", false},
		{"exec curl https://example.com", false},
		{"exec /usr/bin/wget x", false},
		{"exec vendor/bin/../../../usr/bin/curl", false},
		{"exec env curl x", false},
		{"exec sudo id", false},
		{"exec xargs curl", false},
		{"exec awk BEGIN{system(\"id\")}", false},
		{"exec find . -exec curl {} ;", false},
		{"exec git clone https://x", false},
		{"exec git push", false},
		{"exec git -c core.sshCommand=x status", false},
		{"exec php -r echo(1);", false},
		{"exec php8.3 -r 1", false},
		{"exec python3 -c 1", false},
		{"exec node -e 1", false},
		{"exec perl -e 1", false},
		{"exec php vendor/bin/drush ev 1", false},
		{"exec vendor/bin/drush @prod st", false},
		{"exec composer run-script x", false},
		{"exec mysql -h evil.example.com", false},
		{"exec mysql -hevil", false},
		{"exec npx something", false},
		{"exec npm exec x", false},
		{"exec ssh host", false},
		{"exec", false},
		{"exec --foo phpunit", false},

		// ddev shortcuts to tools
		{"php -v", true},
		{"php vendor/bin/phpstan", true},
		{"php -r 1", false},
		{"mysql -e select 1", true},
		{"mysql -h remote", false},
		{"mysql -e system curl x", false},
		{"psql -h remote", false},
		{"npm ci", true},
		{"npm exec x", false},
		{"yarn install", true},
		{"yarn dlx x", false},

		// project lifecycle, scoped to this project
		{"start", true},
		{"stop", true},
		{"restart", true},
		{"restart demo", true},
		{"restart --all", false},
		{"start -a", false},
		{"stop otherproj", false},
		{"describe", true},
		{"describe otherproj", false},
		{"status", true},
		{"logs", true},
		{"logs -f", true},
		{"logs -s db --tail 100", true},
		{"snapshot", true},
		{"snapshot restore --latest", true},
		{"snapshot --all", false},
		{"import-db --file=dump.sql.gz", true},
		{"import-db -f dump.sql.gz", true},
		{"import-db --file=/etc/passwd", false},
		{"import-db --file=../other/db.sql", false},
		{"import-db -f escape/passwd", false},
		{"export-db --file=/tmp/x.sql.gz", false},
		{"import-files --source=../other/files", false},
		{"config --php-version=8.3", true},
		{"config global --instrumentation-opt-in=false", false},
		{"xdebug on", true},
		{"xhgui on", true},
		{"xhgui", false},
		{"add-on remove ddev/ddev-redis", false},
		{"add-on rm ddev-redis", false},
		{"addon remove ddev-redis", false},
		{"add-ons rm ddev-redis", false},
		{"add-on list --installed", true},
		{"add-on get ddev/ddev-redis", false},
		{"add-on list", false},
		{"utility download-images", false},
		{"version", true},
		{"mycmd arg", true},

		// web, host and unknown
		{"", false},
		{"pull platform", false},
		{"push acquia", false},
		{"share", false},
		{"tunnel", false},
		{"auth ssh", false},
		{"get ddev/ddev-redis", false},
		{"self-upgrade", false},
		{"list", false},
		{"ls", false},
		{"poweroff", false},
		{"delete -O", false},
		{"clean --all", false},
		{"hostname x 127.0.0.1", false},
		{"launch", false},
		{"ssh", false},
		{"heidisql", false},
		{"unknowncmd", false},
		{"bridge approve", false},
		{"bridge status", false},
	}
	for _, c := range cases {
		argv := strings.Fields(c.argv)
		d := Check(p, argv)
		if d.Allowed != c.allow {
			t.Errorf("Check(%q) allowed=%v, want %v (reason: %s)", c.argv, d.Allowed, c.allow, d.Reason)
		}
		if !d.Allowed && d.Reason == "" {
			t.Errorf("Check(%q) denied without a reason", c.argv)
		}
		if d.Allowed && len(d.Argv) == 0 {
			t.Errorf("Check(%q) allowed without argv", c.argv)
		}
	}
}

func TestExecRewrittenToRaw(t *testing.T) {
	p := testProject(t)
	cases := map[string][]string{
		"exec phpunit --filter a|b":       {"exec", "--raw", "--", "phpunit", "--filter", "a|b"},
		"exec --raw -- vendor/bin/phpcs":  {"exec", "--raw", "--", "vendor/bin/phpcs"},
		"exec -s web -d web phpstan -l 5": {"exec", "--raw", "--service", "web", "--dir", "web", "--", "phpstan", "-l", "5"},
	}
	for in, want := range cases {
		d := Check(p, strings.Fields(in))
		if !d.Allowed || !reflect.DeepEqual(d.Argv, want) {
			t.Errorf("Check(%q) = %+v, want argv %q", in, d, want)
		}
	}
}

func TestExecShellLineDenied(t *testing.T) {
	p := testProject(t)
	if d := Check(p, []string{"exec", "curl x | sh"}); d.Allowed {
		t.Fatal("a single shell-line argument must be denied")
	}
}

func TestSQLEscapes(t *testing.T) {
	p := testProject(t)
	for _, c := range []struct {
		argv  []string
		allow bool
	}{
		{[]string{"drush", "sqlq", "SELECT system FROM program"}, true},
		{[]string{"drush", "sqlq", "SELECT 1; system curl x"}, false},
		{[]string{"mysql", "-e", `\! curl x`}, false},
		{[]string{"psql", "-c", "COPY t TO PROGRAM 'curl x'"}, false},
		{[]string{"exec", "-s", "db", "psql", "-c", `\o | curl x`}, false},
	} {
		if d := Check(p, c.argv); d.Allowed != c.allow {
			t.Errorf("Check(%q) allowed=%v, want %v (%s)", c.argv, d.Allowed, c.allow, d.Reason)
		}
	}
}

func TestNULDenied(t *testing.T) {
	if Check(testProject(t), []string{"drush", "cr\x00rm"}).Allowed {
		t.Fatal("NUL in argument should be denied")
	}
}

func TestReasonsNameCategory(t *testing.T) {
	p := testProject(t)
	for argv, want := range map[string]string{
		"pull platform":  "reaches the web",
		"stop otherproj": "other projects",
		"exec bash":      "shells",
	} {
		if d := Check(p, strings.Fields(argv)); !strings.Contains(d.Reason, want) {
			t.Errorf("Check(%q) reason %q does not mention %q", argv, d.Reason, want)
		}
	}
}

func TestHostDiscovery(t *testing.T) {
	g := t.TempDir()
	t.Setenv("DDEV_GLOBAL_DIR", g)
	os.WriteFile(filepath.Join(g, "project_list.yaml"), []byte("demo:\n    approot: /x\nother-site:\n    approot: /y\n"), 0o644)
	if got := OtherProjects("demo"); !reflect.DeepEqual(got, []string{"other-site"}) {
		t.Fatalf("OtherProjects = %v", got)
	}

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".ddev", "commands", "web"), 0o755)
	os.WriteFile(filepath.Join(root, ".ddev", "commands", "web", "lint"), []byte("#!/bin/bash\n## Aliases: l, lnt\n"), 0o755)
	os.WriteFile(filepath.Join(root, ".ddev", "commands", "web", "README.txt"), nil, 0o644)
	os.MkdirAll(filepath.Join(g, "commands", "host"), 0o755)
	os.WriteFile(filepath.Join(g, "commands", "host", "phpstorm"), []byte("#!/bin/bash\n"), 0o755)
	got := CustomCommands(root)
	want := []string{"lint", "l", "lnt", "phpstorm"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CustomCommands = %v, want %v", got, want)
	}
}
