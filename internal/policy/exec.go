package policy

import (
	"strings"
)

// Programs that are shells or run arbitrary other programs.
var shells = setOf("bash", "sh", "dash", "ash", "zsh", "fish", "ksh", "mksh", "csh", "tcsh")

var wrappers = setOf("env", "sudo", "su", "doas", "runuser", "xargs", "timeout", "nohup", "nice", "ionice",
	"setsid", "stdbuf", "script", "busybox", "watch", "flock", "strace", "ltrace", "chroot", "unshare",
	"nsenter", "time", "parallel", "expect", "tmux", "screen", "awk", "gawk", "mawk", "nawk")

// Programs whose purpose is talking to the network.
var netTools = setOf("curl", "wget", "ssh", "scp", "sftp", "rsync", "nc", "ncat", "netcat", "telnet",
	"ftp", "lftp", "socat", "aria2c", "http", "https", "ssh-keyscan", "dig", "nslookup", "host")

// Interpreter flags that evaluate inline code.
var evalFlags = map[string][]string{
	"php":    {"-r", "-a", "-B", "-R", "-E", "-F", "--run", "--interactive", "--process-begin", "--process-code", "--process-end", "--process-file"},
	"python": {"-c"},
	"perl":   {"-e", "-E"},
	"ruby":   {"-e"},
	"node":   {"-e", "-p", "--eval", "--print", "-i", "--interactive"},
}

var gitNetwork = setOf("clone", "fetch", "pull", "push", "ls-remote", "remote", "submodule", "send-email",
	"request-pull", "archive", "daemon", "http-fetch", "http-push", "upload-pack", "receive-pack")

func setOf(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// interpreter maps python3.12, php8.3, nodejs, ... to their family name.
func interpreter(name string) string {
	for _, fam := range []string{"python", "php", "perl", "ruby", "node"} {
		if name == fam || name == fam+"js" || strings.HasPrefix(name, fam) && strings.Trim(name[len(fam):], "0123456789.-") == "" {
			return fam
		}
	}
	return ""
}

// checkTool checks running program bin with args inside a ddev container.
// It is used for `ddev exec` and for ddev's tool shortcuts (`ddev composer`, `ddev php`, ...).
func checkTool(p Project, bin string, args []string) Decision {
	name := base(bin)
	switch {
	case shells[name]:
		return deny("%s is not allowed: %s", name, whyShell)
	case wrappers[name]:
		return deny("%s is not allowed: it runs other programs, which would bypass the bridge checks", name)
	case netTools[name]:
		return deny("%s is not allowed: %s", name, whyWeb)
	}

	if fam := interpreter(name); fam != "" {
		if f, ok := hasFlag(args, evalFlags[fam]...); ok {
			return deny("%s %s is not allowed: %s", name, f, whyShell)
		}
		// `php vendor/bin/drush ...`, `php composer.phar ...`: check the script's arguments too.
		if fam == "php" {
			if i := firstPositional(args); i >= 0 {
				script := base(args[i])
				if script == "drush" || script == "drush.php" {
					return checkDrush(p, args[i+1:])
				}
				if script == "composer" || script == "composer.phar" {
					return checkTool(p, "composer", args[i+1:])
				}
			}
		}
		return allow(nil)
	}

	switch name {
	case "drush", "drush.php":
		return checkDrush(p, args)
	case "composer", "composer.phar":
		return checkComposer(args)
	case "mysql", "mariadb", "psql", "pgcli", "mycli":
		if f, ok := hasFlag(args, "-h", "--host", "--pager"); ok {
			return deny("%s %s is not allowed: only the project database is reachable", name, f)
		}
		return checkSQLText(args)
	case "mysqldump", "mariadb-dump", "pg_dump", "pg_dumpall", "pg_restore":
		if f, ok := hasFlag(args, "-h", "--host"); ok {
			return deny("%s %s is not allowed: only the project database is reachable", name, f)
		}
	case "git":
		if f, ok := hasFlag(args, "-c", "--config-env", "--exec-path"); ok {
			return deny("git %s is not allowed: it can run arbitrary commands", f)
		}
		if i := firstPositional(args); i >= 0 && gitNetwork[args[i]] {
			return deny("git %s is not allowed: %s", args[i], whyWeb)
		}
	case "npx", "pnpx", "bunx":
		return deny("%s is not allowed: it downloads and runs packages ad hoc", name)
	case "npm", "pnpm":
		if i := firstPositional(args); i >= 0 {
			switch args[i] {
			case "exec", "x", "explore", "dlx":
				return deny("%s %s is not allowed: it runs arbitrary packages or shells", name, args[i])
			}
		}
	case "yarn":
		if i := firstPositional(args); i >= 0 && (args[i] == "dlx" || args[i] == "exec") {
			return deny("yarn %s is not allowed: it runs arbitrary packages", args[i])
		}
	case "find":
		if f, ok := hasFlag(args, "-exec", "-execdir", "-ok", "-okdir"); ok {
			return deny("find %s is not allowed: it runs other programs", f)
		}
	case "openssl":
		if i := firstPositional(args); i >= 0 && (args[i] == "s_client" || args[i] == "s_server") {
			return deny("openssl %s is not allowed: %s", args[i], whyWeb)
		}
	}
	return allow(nil)
}

// checkComposer allows composer (including package downloads) except arbitrary code and global changes.
func checkComposer(args []string) Decision {
	i := firstPositional(args)
	if i < 0 {
		return allow(nil)
	}
	switch args[i] {
	case "exec", "run-script", "run":
		return deny("composer %s is not allowed: %s", args[i], whyShell)
	case "global", "self-update", "selfupdate":
		return deny("composer %s is not allowed: %s", args[i], whyHost)
	case "config":
		if _, ok := hasFlag(args, "-g", "--global"); ok {
			return deny("composer config --global is not allowed: %s", whyHost)
		}
	}
	if _, ok := hasFlag(args, "-g", "--global"); ok {
		return deny("composer --global is not allowed: %s", whyHost)
	}
	return allow(nil)
}

// checkExec allows `ddev exec [-s SERVICE] [-d DIR] <program> args...` subject to
// checkTool. It rewrites the call to `ddev exec --raw [flags] -- <program> args...`
// so the container does not pass the arguments through `bash -c`.
func checkExec(p Project, argv []string) Decision {
	flags, cmd, d := parseExec(argv)
	if !d.Allowed {
		return d
	}
	if d := checkTool(p, cmd[0], cmd[1:]); !d.Allowed {
		return d
	}
	return allow(execArgv(flags, cmd))
}

// parseExec splits `exec [flags] [--] program args...` into the flags to keep
// and the program with its arguments.
func parseExec(argv []string) (flags, cmd []string, d Decision) {
	args := argv[1:]
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
		name, val, hasVal := splitOpt(a)
		takeVal := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch name {
		case "--raw":
		case "-s", "--service":
			v, ok := takeVal()
			if !ok || !safeValue.MatchString(v) {
				return nil, nil, deny("exec: --service requires a service name")
			}
			flags = append(flags, "--service", v)
		case "-d", "--dir":
			v, ok := takeVal()
			if !ok || !safeValue.MatchString(v) {
				return nil, nil, deny("exec: --dir requires a simple path")
			}
			flags = append(flags, "--dir", v)
		case "-q", "--quiet":
			flags = append(flags, name)
		default:
			return nil, nil, deny("exec: option %q is not allowed", a)
		}
	}
	if i >= len(args) {
		return nil, nil, deny("exec: no command given")
	}
	if strings.ContainsAny(args[i], " \t\n;|&$`<>()") {
		return nil, nil, deny("exec: %q looks like a shell command line; pass the program and its arguments separately (no shell is used)", args[i])
	}
	return flags, args[i:], allow(nil)
}

func execArgv(flags, cmd []string) []string {
	out := append([]string{"exec", "--raw"}, flags...)
	out = append(out, "--")
	return append(out, cmd...)
}
