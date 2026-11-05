package policy

import (
	"regexp"
	"strings"
)

// Drush commands that are denied, with their aliases.
var drushDeniedGroups = map[string][]string{
	// reaches the web
	"sql:sync":        {"sql-sync"},
	"core:rsync":      {"rsync", "core-rsync"},
	"site:ssh":        {"ssh", "site-ssh"},
	"pm:security":     {"sec", "pm-security"},
	"pm:security-php": {"sec-php", "pm-security-php"},
	"self-update":     {"selfupdate", "self:update"},
	"locale:check":    {"locale-check"},
	"locale:update":   {"locale-update"},
	"browse":          nil,
	// shell / eval
	"php:eval":   {"eval", "ev", "php-eval"},
	"php:script": {"scr", "php-script"},
	"php:cli":    {"php", "core:cli", "core-cli"},
}

var drushDenied = func() map[string]string {
	why := map[string]string{}
	for c := range aliasTable(drushDeniedGroups) {
		why[c] = whyWeb
	}
	for _, c := range []string{"php:eval", "eval", "ev", "php-eval", "php:script", "scr", "php-script", "php:cli", "php", "core:cli", "core-cli"} {
		why[c] = whyShell
	}
	return why
}()

// Options that would let drush escape the local site or load foreign code/config.
var drushForbiddenOpts = map[string]bool{
	"-r": true, "--root": true, "--alias-path": true, "-c": true, "--config": true, "--include": true,
	"--ssh-options": true, "--remote-host": true, "--remote-user": true,
	"-D": true, "--define": true, "--php": true, "--php-options": true,
}

// Value-less drush and Symfony flags that may come before the command. Any
// other option there must carry its value with "=": drush and Symfony take the
// value of -l/--uri, -c, --strict, … from the next argument, which the bridge
// would otherwise mistake for the command (`drush -l x php:eval …`).
var drushFlagsBeforeCommand = map[string]bool{
	"--": true, "--yes": true, "--no": true, "--no-interaction": true, "--debug": true,
	"--verbose": true, "--quiet": true, "--simulate": true, "--xdebug": true,
	"--ansi": true, "--no-ansi": true, "--help": true, "--version": true, "--local": true,
}

// Short value-less flags; combined forms like -yv are allowed.
const drushShortFlags = "yndvqshV"

func drushFlagBeforeCommand(a string) bool {
	if strings.Contains(a, "=") || drushFlagsBeforeCommand[a] {
		return true
	}
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return false
	}
	for _, c := range a[1:] {
		if !strings.ContainsRune(drushShortFlags, c) {
			return false
		}
	}
	return true
}

var drushSQLCommands = map[string]bool{"sql:query": true, "sqlq": true, "sql-query": true}

// checkDrush checks drush arguments (without "drush").
func checkDrush(args []string) Decision {
	command := ""
	for _, a := range args {
		if strings.HasPrefix(a, "@") {
			if a == "@self" || a == "@none" {
				continue
			}
			return deny("drush site alias %q is not allowed: %s", a, whyWeb)
		}
		if strings.HasPrefix(a, "-") {
			name, _, _ := splitOpt(a)
			if drushForbiddenOpts[name] {
				return deny("drush option %s is not allowed: it can load code or config from outside the project", name)
			}
			if command == "" && !drushFlagBeforeCommand(a) {
				return deny("drush option %s before the command needs its value attached (%s=<value>), or put it after the command", a, a)
			}
			continue
		}
		if command == "" {
			command = a
		}
	}
	if command == "" {
		return allow(nil) // `drush` alone lists commands
	}
	if why, ok := drushDenied[strings.ToLower(command)]; ok {
		return deny("drush %s is not allowed: %s", command, why)
	}
	if drushSQLCommands[command] {
		if d := checkSQLText(args); !d.Allowed {
			return d
		}
	}
	return allow(nil)
}

// SQL client escapes that run a shell command or a program.
var sqlShellEscape = regexp.MustCompile(`(?i)(\\!|(^|;)\s*system(\s|$)|\\o\s*\||\b(to|from)\s+program\s*['"$])`)

func checkSQLText(args []string) Decision {
	for _, a := range args {
		if sqlShellEscape.MatchString(a) {
			return deny("SQL shell escapes (\\!, system, \\o |, PROGRAM) are not allowed: %s", whyShell)
		}
	}
	return allow(nil)
}
