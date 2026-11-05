// Command ddev-bridge is the host-side daemon that lets sandboxed agents run
// ddev commands in registered projects, without reaching the web or other projects.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
  ddev-bridge ensure [--addr …]                start the bridge in the background unless this version already runs
  ddev-bridge stop [--addr …]                  stop the background bridge
  ddev-bridge status [--addr …]                show whether the bridge runs, and the registered projects
  ddev-bridge version                          print the version
  ddev-bridge install-project [dir]            install the sandbox client into .helga-ai/bin, update
                                               .gitignore, register (used by the ddev add-on)
  ddev-bridge uninstall-project [dir]          remove the client and token, unregister
  ddev-bridge register [dir]                   register a ddev project (default: .), write its token
                                               and approve its current .ddev/ configuration
  ddev-bridge diff [dir]                       show .ddev/ changes awaiting approval
  ddev-bridge approve [dir]                    approve the current .ddev/ configuration after reviewing changes
  ddev-bridge unregister [dir]                 remove a project
  ddev-bridge list                             list registered projects
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
		fs.Parse(os.Args[2:])
		var err error
		switch os.Args[1] {
		case "ensure":
			err = ensure(*addr)
		case "stop":
			err = stop(*addr)
		case "status":
			running := status(*addr)
			listProjects()
			if !running {
				os.Exit(1)
			}
		}
		if err != nil {
			log.Fatal(err)
		}
	case "version", "--version":
		fmt.Println(version)
	case "--addon-marker":
		fmt.Println(addonMarker)
	case "install-project":
		installProject(os.Args[2:])
	case "uninstall-project":
		uninstallProject(os.Args[2:])
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
			log.Fatal(err)
		}
		root, _, err := registry.ValidateRoot(dir)
		if err != nil {
			log.Fatal(err)
		}
		found := false
		for _, p := range reg.Projects {
			if p.Root != root {
				continue
			}
			found = true
			fp, err := integrity.Snapshot(root)
			if err != nil {
				log.Fatal(err)
			}
			diff := integrity.Diff(p.Approved, fp)
			if len(diff) == 0 {
				fmt.Printf("%s: .ddev/ matches the approved state (%s)\n", p.Name, p.ApprovedAt)
			}
			for _, d := range diff {
				fmt.Println(d)
			}
			if len(diff) > 0 {
				fmt.Println("review the files (e.g. git diff .ddev/) before approving")
				os.Exit(1)
			}
		}
		if !found {
			log.Fatalf("%s is not registered", root)
		}
	case "list":
		listProjects()
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
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

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "listen address (keep it on loopback)")
	timeout := fs.Duration("timeout", 10*time.Minute, "per-command timeout")
	fs.Parse(args)

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
