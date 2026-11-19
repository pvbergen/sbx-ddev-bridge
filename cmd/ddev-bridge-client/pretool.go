package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"ddev-bridge/internal/sqlread"
)

// preToolUse implements the Claude Code PreToolUse hook for Bash. It finds the
// calls that run SQL (`drush sql:query`, `ddev mysql -e`, and the same through
// `ddev drush` or `ddev exec`). If any of them is not a plain read, it asks
// the user; otherwise it prints nothing and the usual permission rules apply.
// It never allows anything by itself. If the input can't be understood and
// mentions SQL, it asks.
func preToolUse(r io.Reader, w io.Writer) error {
	data, readErr := io.ReadAll(r)
	var in struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	reason := ""
	if err := json.Unmarshal(data, &in); readErr != nil || err != nil {
		if readErr != nil || sqlish.Match(data) {
			reason = "could not read the hook input"
		}
	} else if in.ToolName == "Bash" {
		reason = checkShell(in.ToolInput.Command)
	}
	if reason == "" {
		return nil
	}
	return json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "ask",
		"permissionDecisionReason": "ddev-bridge: not a read-only query: " + reason,
	}})
}

// sqlish marks text that may run SQL; everything else is left alone.
var sqlish = regexp.MustCompile(`(?i)sql|mariadb`)

// checkShell returns why command needs confirmation, or "".
func checkShell(command string) string {
	if !sqlish.MatchString(command) && !strings.Contains(command, "drush") && !strings.Contains(command, "ddev") {
		return ""
	}
	cmds, err := lexShell(command)
	if err != nil {
		return "could not parse the shell command (" + err.Error() + ")"
	}
	for _, c := range cmds {
		if r := checkSimple(c); r != "" {
			return r
		}
	}
	return ""
}

// shWord is one shell word after quote removal. dynamic marks words whose
// value is only known at run time ($VAR, $(…), globs).
type shWord struct {
	text    string
	dynamic bool
}

// wrappers run another command we don't look into.
var wrappers = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "eval": true, "exec": true, "xargs": true,
	"env": true, "timeout": true, "time": true, "nice": true, "nohup": true, "command": true,
	"sudo": true, "su": true, "watch": true, "parallel": true, "script": true, "stdbuf": true,
}

// shellKeywords may precede a command.
var shellKeywords = map[string]bool{"!": true, "{": true, "}": true, "if": true, "then": true,
	"else": true, "elif": true, "do": true, "while": true, "until": true, "time": true}

// checkSimple checks one simple command.
func checkSimple(words []shWord) string {
	anySQL := false
	for _, w := range words {
		anySQL = anySQL || sqlish.MatchString(w.text) || w.dynamic
	}
	if !anySQL {
		return ""
	}
	for len(words) > 0 && (shellKeywords[words[0].text] || isAssignment(words[0].text)) {
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}
	prog := words[0]
	if prog.dynamic {
		return "the command name is only known at run time"
	}
	switch name := path.Base(prog.text); {
	case name == "drush":
		return checkDrush(words[1:])
	case name == "ddev" && len(words) > 1:
		switch words[1].text {
		case "drush":
			return checkDrush(words[2:])
		case "mysql", "mariadb":
			return checkMySQL(words[2:])
		case "exec":
			return checkExec(words[2:])
		}
	case wrappers[name]:
		return fmt.Sprintf("can't tell what `%s` runs", name)
	}
	return ""
}

func isAssignment(s string) bool {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return false
	}
	for j, c := range s[:i] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// checkExec handles `ddev exec [flags] <program> …`.
func checkExec(args []shWord) string {
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case a == "-s" || a == "--service" || a == "-d" || a == "--dir":
			i++
		case strings.HasPrefix(a, "--service=") || strings.HasPrefix(a, "--dir=") || a == "--raw" || a == "-q" || a == "--quiet":
		case strings.HasPrefix(a, "-"):
			return fmt.Sprintf("unknown `ddev exec` option %s", a)
		default:
			if args[i].dynamic {
				return "the command name is only known at run time"
			}
			switch path.Base(a) {
			case "drush":
				return checkDrush(args[i+1:])
			case "mysql", "mariadb":
				return checkMySQL(args[i+1:])
			}
			return ""
		}
	}
	return ""
}

// isSQLQueryName reports whether drush may resolve name to sql:query: the
// names and aliases themselves, or a Symfony abbreviation of them.
func isSQLQueryName(name string) bool {
	n := strings.ToLower(name)
	if seg := strings.Split(n, ":"); len(seg) == 2 {
		return seg[0] != "" && seg[1] != "" && strings.HasPrefix("sql", seg[0]) && strings.HasPrefix("query", seg[1])
	}
	return strings.HasPrefix(n, "sql") && (strings.HasPrefix("sqlq", n) || strings.HasPrefix("sql-query", n))
}

// drushOK are the options that don't change what sql:query runs.
var drushOK = map[string]bool{"-y": true, "--yes": true, "-n": true, "--no-interaction": true,
	"-v": true, "-vv": true, "-vvv": true, "--verbose": true, "-q": true, "--quiet": true,
	"-d": true, "--debug": true, "--ansi": true, "--no-ansi": true, "--db-prefix": true}

var drushOKValue = []string{"--uri=", "-l=", "--database=", "--root=", "-r="}

func checkDrush(args []shWord) string {
	var pos []shWord
	var opts []string
	for _, a := range args {
		if strings.HasPrefix(a.text, "-") {
			opts = append(opts, a.text)
		} else {
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 {
		return ""
	}
	if pos[0].dynamic {
		return "the drush command is only known at run time"
	}
	if !isSQLQueryName(pos[0].text) {
		return ""
	}
	for _, o := range opts {
		if !drushOK[o] && !hasAnyPrefix(o, drushOKValue) {
			return fmt.Sprintf("option %s", o)
		}
	}
	switch q := pos[1:]; {
	case len(q) == 0:
		return "the query comes from stdin or a file"
	case len(q) > 1:
		return "more than one query argument"
	case q[0].dynamic:
		return "the query is only known at run time"
	default:
		return sqlread.Check(q[0].text)
	}
}

// mysqlFlags take no value; mysqlValue take one.
var mysqlFlags = map[string]bool{"N": true, "B": true, "t": true, "s": true, "r": true, "E": true, "H": true, "X": true,
	"--skip-column-names": true, "--batch": true, "--table": true, "--silent": true, "--raw": true,
	"--vertical": true, "--html": true, "--xml": true, "--column-names": true}

var mysqlValue = map[string]bool{"e": true, "D": true, "d": true, "--execute": true, "--database": true}

func checkMySQL(args []shWord) string {
	var queries []shWord
	take := func(opt string, val shWord) {
		if opt == "e" || opt == "--execute" {
			queries = append(queries, val)
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (shWord, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return shWord{}, false
		}
		switch {
		case a.dynamic && !strings.HasPrefix(a.text, "-"):
			return "an argument is only known at run time"
		case strings.HasPrefix(a.text, "--"):
			name, val, hasVal := strings.Cut(a.text, "=")
			switch {
			case mysqlFlags[name] && !hasVal:
			case mysqlValue[name] && hasVal:
				take(name, shWord{text: val, dynamic: a.dynamic})
			case mysqlValue[name]:
				v, ok := next()
				if !ok {
					return fmt.Sprintf("option %s without a value", name)
				}
				take(name, v)
			default:
				return fmt.Sprintf("option %s", name)
			}
		case strings.HasPrefix(a.text, "-") && len(a.text) > 1:
			for j := 1; j < len(a.text); j++ {
				c := a.text[j : j+1]
				if mysqlFlags[c] {
					continue
				}
				if !mysqlValue[c] {
					return fmt.Sprintf("option -%s", c)
				}
				if j+1 < len(a.text) {
					take(c, shWord{text: a.text[j+1:], dynamic: a.dynamic})
				} else if v, ok := next(); ok {
					take(c, v)
				} else {
					return fmt.Sprintf("option -%s without a value", c)
				}
				break
			}
		default:
			return fmt.Sprintf("argument %s", a.text)
		}
	}
	if len(queries) == 0 {
		return "the query comes from stdin"
	}
	for _, q := range queries {
		if q.dynamic {
			return "the query is only known at run time"
		}
		if r := sqlread.Check(q.text); r != "" {
			return r
		}
	}
	return ""
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
