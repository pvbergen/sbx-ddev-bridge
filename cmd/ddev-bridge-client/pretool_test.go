package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCheckShell(t *testing.T) {
	noPrompt := []string{
		`drush sql:query "SELECT 1"`,
		`drush sqlq "SELECT nid FROM node; SELECT COUNT(*) FROM users"`,
		`drush sql-query 'SHOW TABLES'`,
		`drush sql:q "SELECT 1"`,
		`drush SQL:QUERY "SELECT 1"`,
		`ddev drush sql:query "SELECT 1"`,
		`ddev drush -y --uri=default sql:query --database=default "SELECT 1"`,
		`.ddev-bridge/bin/drush sqlq "SELECT 1"`,
		`ddev exec drush sqlq "SELECT 1"`,
		`ddev exec -s web --raw vendor/bin/drush sqlq "SELECT 1"`,
		`ddev mysql -e "SELECT 1; SELECT 2"`,
		`ddev mysql -NBe "SELECT 1"`,
		`ddev mysql -N -B --execute="SELECT 1"`,
		`ddev mariadb --database=db --execute 'SHOW TABLES'`,
		`ddev mysql -e"SELECT 1"`,
		`ddev exec mysql -e "SELECT 1"`,
		`drush sqlq "SELECT 1" | head -20`,
		`drush sqlq "SELECT 1" 2>&1 | grep x > out.txt`,
		`cd web && drush sqlq "SELECT 1" && echo done`,
		"drush sqlq \\\n  \"SELECT 1\"",
		`drush sqlq "SELECT 'it''s'"`,
		`drush sqlq "SELECT \"x\" FROM t"`,
		`drush sqlq 'SELECT "$x"'`,
		`echo "$(drush sqlq 'SELECT 1')"`,
		`FOO=1 drush sqlq "SELECT 1"`,
		// simple wrappers: the wrapped command is checked
		`timeout 10 drush sqlq "SELECT 1"`,
		`timeout -k 5 --signal=TERM 120 chromium --screenshot=$PWD/$n.png https://x.ddev.site`,
		`timeout 120 curl -sk -o $n.html "https://x.ddev.site$u"`,
		`nice -n 10 nohup ddev exec drush sqlq "SELECT 1"`,
		`nice -10 drush cr`,
		`time -p drush sqlq "SELECT 1"`,
		`stdbuf -oL ddev mysql -e "SELECT 1"`,
		`env -u X FOO=$BAR drush sqlq "SELECT 1"`,
		`command drush sqlq "SELECT 1"`,
		`exec -a x drush sqlq "SELECT 1"`,
		`timeout -- 10 ddev logs`,
		// [ and [[ are test commands, not globs
		`drush cr; [ -f "$f" ] || echo missing`,
		`ddev ls; [[ -n "$f" ]] && echo ok`,
		// not SQL queries: other rules apply
		`drush cr`,
		`drush sql:dump --result-file=../x.sql`,
		`ddev import-db --file=dump.sql`,
		`grep -r sqlq src/`,
		`git commit -m "use sql:query"`,
		`ls $DIR`,
		`ddev exec ls`,
		`ddev describe`,
		// here-document bodies are text, not commands
		"gh pr create --title x --body \"$(cat <<'EOF'\nFix `$settings['trusted_host_patterns']` for *.ddev.site.\nIt doesn't touch SQL (or drush).\nEOF\n)\"",
		"cat <<EOF > notes.md\nddev mysql isn't run here; neither is `ls`\nEOF\ndrush sqlq \"SELECT 1\"",
		"cat <<-\"EOF\"\n\tddev mysql -e 'DROP TABLE x'\n\tEOF\necho done",
		"cat <<\\EOF\n$(drush sqlq 'DELETE FROM x')\nEOF",
		"cat <<A <<B\nddev mysql it's\nA\nddev mysql it's\nB",
		"cat <<'EOF'\nddev mysql it's\nEOF",
	}
	for _, c := range noPrompt {
		if r := checkShell(c); r != "" {
			t.Errorf("checkShell(%q) = %q, want no prompt", c, r)
		}
	}

	ask := []string{
		`drush sqlq "DELETE FROM users"`,
		`drush sqlq "SELECT 1; DELETE FROM users"`,
		`drush sql:query "UPDATE users SET status = 0"`,
		`ddev drush sqlq "TRUNCATE cache_render"`,
		`drush sqlq --file=q.sql`,
		`drush sqlq --extra=--init-command=x "SELECT 1"`,
		`drush sqlq --result-file=x "SELECT 1"`,
		`drush sqlq`,
		`drush sqlq < q.sql`,
		`drush sqlq <<< "DELETE FROM x"`,
		`drush sqlq "SELECT 1" "SELECT 2"`,
		`drush sqlq "$Q"`,
		`drush sqlq "SELECT $(cat x)"`,
		`drush sqlq SELECT\ *\ FROM\ x`,
		`drush $CMD "DELETE FROM x"`,
		`dru[s]h sqlq "DELETE FROM x"`,
		`ddev mysql`,
		`ddev mysql < dump.sql`,
		`ddev mysql -e "DROP TABLE x"`,
		`ddev mysql -e "SELECT 1" -e "DELETE FROM x"`,
		`ddev mysql --init-command="DELETE FROM x" -e "SELECT 1"`,
		`ddev mysql db -e "SELECT 1"`,
		`ddev mysql -e`,
		`ddev mariadb -e 'INSERT INTO t VALUES (1)'`,
		`ddev exec mysql -e "DELETE FROM x"`,
		`ddev exec --bogus drush sqlq "SELECT 1"`,
		`drush sqlq "SELECT 1" && drush sqlq "DELETE FROM x"`,
		`drush sqlq "SELECT 1"; ddev mysql -e "DELETE FROM x"`,
		`echo $(drush sqlq "DELETE FROM x")`,
		"echo `drush sqlq 'DELETE FROM x'`",
		`cat <(ddev mysql -e "DELETE FROM x")`,
		`(cd web; drush sqlq "DELETE FROM x")`,
		`bash -c 'drush sqlq "DELETE FROM x"'`,
		`xargs drush sqlq < queries.txt`,
		`timeout 10 drush sqlq "DELETE FROM x"`,
		`timeout $T drush sqlq "SELECT 1"`,
		`timeout --bogus 10 drush sqlq "SELECT 1"`,
		`nice -n 5 nohup ddev mysql -e "DROP TABLE x"`,
		`time -p drush sqlq "DELETE FROM x"`,
		`time drush sqlq "DELETE FROM x"`,
		`env FOO=1 drush sqlq "DELETE FROM x"`,
		`env -S 'drush sqlq "DELETE FROM x"'`,
		`env $X drush sqlq "SELECT 1"`,
		`command $CMD sqlq "DELETE FROM x"`,
		`stdbuf -o $M ddev mysql -e "SELECT 1"`,
		`timeout 10 bash -c 'drush sqlq "DELETE FROM x"'`,
		`drush sqlq "SELECT 1`,
		`echo $(drush sqlq "SELECT 1"`,
		"ddev mysql <<EOF\nSELECT 1\nEOF",
		"drush sqlq <<'EOF'\nSELECT 1\nEOF",
		"cat <<EOF\n$(drush sqlq 'DELETE FROM x')\nEOF",
		"cat <<EOF\n`ddev mysql -e 'DROP TABLE x'`\nEOF",
		"cat <<EOF; drush sqlq \"DELETE FROM x\"\nbody\nEOF",
		"cat <<'EOF'\nbody\nEOF\ndrush sqlq \"DELETE FROM x\"",
		"cat <<-EOF\n\tbody\n\tEOF\nddev mysql -e 'DROP TABLE x'",
		"cat <<EOF\nddev mysql never ends",
		"cat <<$X\nddev mysql\n$X",
	}
	for _, c := range ask {
		if r := checkShell(c); r == "" {
			t.Errorf("checkShell(%q) passed without a prompt", c)
		}
	}
}

func TestPreToolUse(t *testing.T) {
	run := func(in string) string {
		t.Helper()
		var out bytes.Buffer
		if err := preToolUse(strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	bash := func(cmd string) string {
		b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": cmd}})
		return string(b)
	}

	if out := run(bash(`drush sqlq "SELECT 1; SELECT 2"`)); out != "" {
		t.Errorf("read: got %s, want no output", out)
	}
	if out := run(`{"tool_name":"Edit","tool_input":{"file_path":"x.sql"}}`); out != "" {
		t.Errorf("other tool: got %s, want no output", out)
	}

	var got struct {
		HookSpecificOutput struct {
			HookEventName, PermissionDecision, PermissionDecisionReason string
		}
	}
	out := run(bash(`drush sqlq "DELETE FROM users"`))
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("write: %v in %q", err, out)
	}
	h := got.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "ask" || !strings.Contains(h.PermissionDecisionReason, "DELETE") {
		t.Errorf("write: got %+v", h)
	}

	if out := run(`{not json, drush sqlq`); !strings.Contains(out, `"ask"`) {
		t.Errorf("bad input mentioning SQL: got %q, want ask", out)
	}
	if out := run(`{not json`); out != "" {
		t.Errorf("bad input without SQL: got %q, want no output", out)
	}
}
