// Command ddev-bridge is the host-side daemon that lets sandboxed agents run
// ddev commands in registered projects, without reaching the web or other projects.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"ddev-bridge/internal/integrity"
	"ddev-bridge/internal/registry"
	"ddev-bridge/internal/server"
)

// version is set at build time (make release).
var version = "dev"

const defaultAddr = "127.0.0.1:7474"

const usage = `ddev-bridge — host-side bridge for sandboxed agents

Usage:
  ddev-bridge serve [--addr 127.0.0.1:7474]   run the bridge in the foreground
  ddev-bridge setup [--global-dir dir]         install or update the bridge for this machine (run by install.sh)
  ddev-bridge uninstall [--global-dir dir]     remove the machine-wide install (after disabling every project)
  ddev-bridge ensure [--addr …] [--project dir]
                                               start the bridge in the background unless this version already runs;
                                               with --project, only if that project is enabled (the ddev hook)
  ddev-bridge stop [--addr …]                  stop the background bridge
  ddev-bridge status [--addr …]                show whether the bridge runs, and the registered projects
  ddev-bridge version                          print the version
  ddev-bridge enable [--no-sbx] [dir]          enable a project: sandbox client in .ddev-bridge/bin, token,
                                               .git/info/exclude, Claude Code overlay, register, approve .ddev/
  ddev-bridge disable [dir]                    undo enable: remove the client, token and overlay, unregister
  ddev-bridge register [dir]                   register a ddev project (default: .), write its token
                                               and approve its current .ddev/ configuration
  ddev-bridge diff [dir]                       show .ddev/ changes awaiting approval (exit 1: changes, 2: error)
  ddev-bridge approve [dir]                    approve the current .ddev/ configuration after reviewing changes
  ddev-bridge unregister [dir]                 remove a project
  ddev-bridge list                             list registered projects
  ddev-bridge policy ls|allow|deny|rm|reset … show or edit the host policy rules (see policy --help)
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "ensure", "stop", "status":
		fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		addr := fs.String("addr", defaultAddr, "bridge address")
		var project *string
		synopsis := "[--addr …]"
		if os.Args[1] == "ensure" {
			project = fs.String("project", "", "start only if this project is enabled")
			synopsis = "[--addr …] [--project dir]"
		}
		mustParse(fs, os.Args[2:], 0, synopsis)
		var err error
		switch os.Args[1] {
		case "ensure":
			if *project != "" && !enabled(*project) {
				return // the global hook runs for every ddev project
			}
			err = ensure(*addr)
		case "stop":
			err = stop(*addr)
		case "status":
			running := status(*addr)
			policySummary(os.Stdout)
			listProjects()
			if !running {
				os.Exit(1)
			}
		}
		if err != nil {
			log.Fatal(err)
		}
	case "setup":
		setupCmd(os.Args[2:])
	case "uninstall":
		uninstallCmd(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
	case "enable", "install-project": // old names, used by the old add-on's actions
		enableProject(os.Args[2:])
	case "disable", "uninstall-project":
		disableProject(os.Args[2:])
	case "register", "unregister", "approve":
		dir := "."
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		reg, err := registry.Load()
		if err != nil {
			log.Fatal(err)
		}
		if os.Args[1] == "register" {
			p, err := reg.Register(dir)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("registered %q at %s\ntoken written to %s/%s (keep it out of git)\n", p.Name, p.Root, p.Root, registry.TokenFile)
		} else if os.Args[1] == "approve" {
			p, diff, err := reg.Approve(dir)
			if err != nil {
				log.Fatal(err)
			}
			if len(diff) == 0 {
				fmt.Printf("%s: .ddev/ unchanged, nothing to approve\n", p.Name)
			} else {
				fmt.Printf("%s: approved .ddev/ changes:\n", p.Name)
				for _, d := range diff {
					fmt.Println("  " + d)
				}
			}
		} else {
			if err := reg.Unregister(dir); err != nil {
				log.Fatal(err)
			}
			fmt.Println("unregistered")
		}
	case "diff":
		dir := "."
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		reg, err := registry.Load()
		if err != nil {
			diffError(err)
		}
		root, _, err := registry.ValidateRoot(dir)
		if err != nil {
			diffError(err)
		}
		found := false
		for _, p := range reg.Projects {
			if p.Root != root {
				continue
			}
			found = true
			fp, err := integrity.Snapshot(root)
			if err != nil {
				diffError(err)
			}
			diff := integrity.Diff(p.Approved, fp)
			if len(diff) == 0 {
				fmt.Printf("%s: .ddev/ matches the approved state (%s)\n", p.Name, p.ApprovedAt)
			}
			for _, d := range diff {
				fmt.Println(d)
			}
			if len(diff) > 0 {
				if hint := integrity.AddonManifestHint(diff); hint != "" {
					fmt.Println(hint)
				}
				fmt.Println("review the files (e.g. git diff .ddev/) before approving")
				os.Exit(1)
			}
		}
		if !found {
			diffError(fmt.Errorf("%s is not registered", root))
		}
	case "list":
		listProjects()
	case "policy":
		os.Exit(policyCmd(os.Args[2:], os.Stdin, os.Stdout))
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

// enabled reports whether dir is a registered project.
func enabled(dir string) bool {
	reg, err := registry.Load()
	if err != nil {
		log.Fatal(err)
	}
	_, ok := reg.Find(dir)
	return ok
}

func listProjects() {
	reg, err := registry.Load()
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range reg.Projects {
		fmt.Printf("%s\t%s\n", p.Name, p.Root)
	}
}

// parseFlags parses args and returns the positional arguments (at most maxPos).
// Go's flag package stops at the first non-flag argument, so a flag written
// after it (`enable ~/proj --no-sbx`) would be silently ignored;
// that is reported as an error instead.
func parseFlags(fs *flag.FlagSet, args []string, maxPos int) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	pos := fs.Args()
	for _, a := range pos {
		if strings.HasPrefix(a, "-") && a != "-" {
			return nil, fmt.Errorf("flag %s comes after an argument; put flags first", a)
		}
	}
	if len(pos) > maxPos {
		return nil, fmt.Errorf("unexpected argument %q", pos[maxPos])
	}
	return pos, nil
}

// mustParse is parseFlags that prints the error and usage and exits 2.
func mustParse(fs *flag.FlagSet, args []string, maxPos int, synopsis string) []string {
	pos, err := parseFlags(fs, args, maxPos)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ddev-bridge %s: %v\nusage: ddev-bridge %s %s\n", fs.Name(), err, fs.Name(), synopsis)
		os.Exit(2)
	}
	return pos
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "listen address (keep it on loopback)")
	timeout := fs.Duration("timeout", 10*time.Minute, "per-command timeout")
	mustParse(fs, args, 0, "[--addr …] [--timeout …]")

	s := &server.Server{Version: version}
	s.Runner.Timeout = *timeout
	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("ddev-bridge %s listening on %s", version, *addr)
	log.Fatal(srv.ListenAndServe())
}

// diffError exits with 2, so callers can tell an error from "changes found" (1).
func diffError(err error) {
	log.Print(err)
	os.Exit(2)
}
