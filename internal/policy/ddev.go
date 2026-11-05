package policy

import "strings"

// ddev subcommands (and aliases) that are denied outright.
var ddevDenied = map[string]string{
	// reaches the web
	"pull":         whyWeb,
	"push":         whyWeb,
	"share":        whyWeb,
	"tunnel":       whyWeb,
	"auth":         whyWeb,
	"self-upgrade": whyWeb,
	"get":          whyWeb, // legacy `ddev get` = add-on get
	// host or other projects
	"poweroff": whyHost, "powerdown": whyHost,
	"clean":  whyHost,
	"delete": whyHost,
	"list":   whyHost, "ls": whyHost,
	"hostname": whyHost,
	"launch":   whyHost,
	"mailpit":  whyHost, // opens a browser on the host
	// host GUI database clients
	"heidisql": whyHost, "sequelace": whyHost, "sequelpro": whyHost,
	"tableplus": whyHost, "dbeaver": whyHost, "querious": whyHost,
	// the bridge's own host command: the sandbox must not approve its own .ddev/ changes
	"bridge": "the bridge is managed by the developer on the host",
	// shell
	"ssh": whyShell,
}

// Known ddev built-ins that are allowed (subject to the per-command checks).
var ddevAllowed = aliasTable(map[string][]string{
	"start": nil, "stop": {"rm", "remove"}, "restart": nil, "pause": {"sp"},
	"describe": {"status", "st", "desc"},
	"logs":     nil, "snapshot": nil,
	"export-db": nil, "import-db": nil, "import-files": nil,
	"mutagen": nil, "config": nil, "service": nil,
	"xdebug": nil, "xhprof": nil, "xhgui": nil,
	"utility": {"ut", "debug", "d"},
	"dotenv":  nil, "add-on": {"addon", "add-ons", "addons"},
	"version": nil, "help": nil, "completion": nil,
	// pass-through to tools in the containers
	"exec": {"."}, "drush": nil, "composer": nil, "php": nil,
	"mysql": nil, "mariadb": nil, "psql": nil,
	"npm": nil, "yarn": nil, "nvm": nil,
})

// Subcommands whose -a / --all acts on every project on the host.
var ddevAllFlag = map[string]bool{"start": true, "stop": true, "restart": true, "pause": true, "snapshot": true}

// Subcommands with options that name a host file.
var ddevFileOpts = map[string][]string{
	"import-db":    {"-f", "--file"},
	"export-db":    {"-f", "--file"},
	"import-files": {"-s", "--source", "--src"},
}

func checkDdev(p Project, argv []string) Decision {
	cmd, args := argv[0], argv[1:]
	if why, ok := ddevDenied[cmd]; ok {
		return deny("ddev %s is not allowed: %s", cmd, why)
	}
	canonical, known := ddevAllowed[cmd]
	if !known {
		for _, c := range p.CustomCommands {
			if c == cmd {
				return allow(argv) // approved custom command (see the .ddev/ approval check)
			}
		}
		return deny("ddev %q is not a known ddev command and not a custom command of this project", cmd)
	}

	switch canonical {
	case "exec":
		return checkExec(argv)
	case "drush":
		if d := checkDrush(args); !d.Allowed {
			return d
		}
		return allow(argv)
	case "composer", "php", "mysql", "mariadb", "psql", "npm", "yarn":
		if d := checkTool(canonical, args); !d.Allowed {
			return d
		}
		return allow(argv)
	case "nvm":
		return allow(argv)
	}

	// Commands that run on the host side of ddev: scope them to this project.
	if ddevAllFlag[canonical] {
		if _, ok := hasFlag(args, "-a", "--all"); ok {
			return deny("ddev %s --all is not allowed: %s", cmd, whyHost)
		}
	}
	for _, a := range args {
		_, val, hasVal := splitOpt(a)
		v := a
		if hasVal {
			v = val
		}
		for _, other := range p.OtherProjects {
			if v == other && other != p.Name {
				return deny("ddev %s %s is not allowed: %s", cmd, a, whyHost)
			}
		}
		if looksLikeHostPath(v) && !insideRoot(p.Root, v) {
			return deny("ddev %s: path %q is outside the project: %s", cmd, v, whyHost)
		}
	}
	if opts, ok := ddevFileOpts[canonical]; ok {
		if d := checkFileOpts(p, cmd, args, opts); !d.Allowed {
			return d
		}
	}

	sub := ""
	if i := firstPositional(args); i >= 0 {
		sub = args[i]
	}
	switch canonical {
	case "config":
		if sub == "global" {
			return deny("ddev config global is not allowed: %s", whyHost)
		}
	case "add-on":
		switch sub {
		case "remove", "rm":
		case "list", "ls":
			if _, ok := hasFlag(args, "--installed"); !ok {
				return deny("ddev add-on list is only allowed with --installed: %s", whyWeb)
			}
		default:
			return deny("ddev add-on %s is not allowed: %s", sub, whyWeb)
		}
	case "utility":
		switch sub {
		case "download-images", "test", "dockercheck":
			return deny("ddev %s %s is not allowed: %s", cmd, sub, whyWeb)
		}
	case "xhgui":
		if sub == "" || sub == "launch" {
			return deny("ddev xhgui without on/off/status opens a browser on the host: %s", whyHost)
		}
	}
	return allow(argv)
}

// checkFileOpts requires host-file options to stay inside the project (after symlinks).
func checkFileOpts(p Project, cmd string, args []string, opts []string) Decision {
	for i := 0; i < len(args); i++ {
		name, val, hasVal := splitOpt(args[i])
		for _, o := range opts {
			if name != o {
				continue
			}
			if !hasVal {
				if i+1 >= len(args) {
					return allow(nil)
				}
				i++
				val = args[i]
			}
			if !insideRoot(p.Root, val) {
				return deny("ddev %s %s %q is outside the project: %s", cmd, o, val, whyHost)
			}
		}
		if strings.HasPrefix(args[i], "-") && len(args[i]) > 2 && args[i][1] != '-' {
			for _, o := range opts {
				if len(o) == 2 && strings.HasPrefix(args[i], o) && !insideRoot(p.Root, args[i][2:]) {
					return deny("ddev %s %s %q is outside the project: %s", cmd, o, args[i][2:], whyHost)
				}
			}
		}
	}
	return allow(nil)
}
