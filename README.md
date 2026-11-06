# ddev-bridge

Lets an AI agent in a Docker sandbox (`sbx`) use ddev **for its own project** on the host, without
moving ddev into the sandbox. The bridge runs ddev and drush on the agent's behalf and refuses what would
**reach the web**, **touch the host or other projects**, or **open a shell / evaluate inline code**.

```
sandbox: ddev / drush (client) ──HTTP──► host.docker.internal:7474
host:    ddev-bridge ─► token → project ─► .ddev/ approved? ─► policy ─► ddev <argv> (no shell)
                                           ◄── live output + exit code
```

Installed once per machine, enabled per project. Details: [reference](docs/reference.md).

## Requirements
- Host: Linux, WSL2 or macOS 12+, with ddev v1.25.5 or newer.
- Agent: in an `sbx` sandbox in **direct mode** (in clone mode the client refuses, because host ddev
  wouldn't see the agent's edits).

## Install
Once per machine, on the host. The release tarball contains the binaries (the repository has none):
```bash
gh release download -R pvbergen/sbx-ddev-bridge -p ddev-bridge.tar.gz -D /tmp --clobber
tar -xzf /tmp/ddev-bridge.tar.gz -C /tmp && /tmp/ddev-bridge/install.sh
```
This installs the binaries and the `ddev bridge` command into ddev's global dir, adds a global
`post-start` hook so `ddev start` starts the bridge for enabled projects, allows `localhost:7474` in the
sbx network policy, and starts the bridge. If ddev's `global_config.yaml` already has hooks of your own,
nothing is installed and the task to add by hand is printed.

**Update:** the same two commands again. Tokens, approvals and your rules stay; enabled projects get the
new client.

**From the old per-project add-on:** run `ddev add-on remove ddev-bridge` in each project first, then
install as above and enable each project. If the removal left `.ddev/` changes, `ddev bridge approve`.

## Enable a project
On the host, in each project the agent may use:
```bash
ddev bridge enable
```
This puts the sandbox client in `.ddev-bridge/` (gitignored), registers the project, approves its current
`.ddev/`, adds the [Claude Code overlay](#agents) and starts the bridge. Nothing goes into `.ddev/`.
`ddev bridge disable` undoes it.

## Daily use
The agent calls `ddev …` and `drush …` as usual. Everything local to the project is allowed: any drush
command, composer, `ddev exec`, start/stop/restart, logs, snapshots, database import/export, the project's
custom commands. A denied call exits with **126** and the reason.

| Denied | Examples |
|---|---|
| Reaches the web | `ddev pull`/`push`/`share`, drush site aliases (`@prod`), `sql:sync`, `curl`, `wget`, `ssh`, `git clone` |
| Host / other projects | `ddev poweroff`/`delete`/`list`, `--all`, other project names, paths outside the project, `add-on remove` |
| Shell / eval | `ddev ssh`, `bash`, `php -r`, `drush php:eval`, `composer run-script` |

Full list: [reference](docs/reference.md#what-is-denied).

**`.ddev/` approval.** ddev runs hooks and host commands from `.ddev/` on the host, and the agent can edit
it. So when `.ddev/` changes, the bridge **pauses** the project and lists the changed files. Review and
approve on the host:
```bash
ddev bridge diff      # what changed since the last approval
ddev bridge approve   # shows the diff, asks, approves
```
Expect this after `ddev config` and after `ddev add-on get`/`remove`. Symlinks in `.ddev/` can't be approved.

**Your own rules** (host policy) go in a plain-text file, managed with `ddev bridge policy`:
```bash
ddev bridge policy deny --reason "reaches live" ddev get_dump   # this project
ddev bridge policy deny --global drush sql:drop                 # every project
ddev bridge policy                                              # list
```
The last matching rule wins, even over the built-in denials. Custom commands are allowed by default:
deny those that reach remote servers. Syntax: [reference](docs/reference.md#host-policy).

**Check on it:** `ddev bridge status` (running, version, projects), `ddev bridge log` (startup and audit log).
All commands: [reference](docs/reference.md#ddev-bridge-commands).

## Agents
**Claude Code** needs no setup. `ddev bridge enable` adds to `.claude/settings.local.json`:
- a `SessionStart` hook that, in a sandbox, puts `.ddev-bridge/bin` on PATH and gives the agent the
  [agent guide](cmd/ddev-bridge-client/agent-guide.md) and the bridge status (on the host it does nothing);
- `permissions.ask` rules for commands that change data, config, dependencies or the environment
  (`drush cim`, `ddev composer require`, `ddev restart`, …). These also apply to host sessions.

If the file isn't valid JSON, it is left alone and the snippet is printed.

**Other agents:** put `.ddev-bridge/bin` first on PATH and give the agent the
[agent guide](cmd/ddev-bridge-client/agent-guide.md).

## Uninstall
**One project:** `ddev bridge disable` in the project. It removes `.ddev-bridge/`, the `.gitignore` line and
the Claude Code overlay, and unregisters the project. For a project directory that's already gone, run
the binary directly: `<ddev global dir>/ddev-bridge-dist/host/<os>-<arch>/ddev-bridge disable <dir>`.

**The machine**, after disabling every project (it refuses otherwise and lists them):
```bash
~/.ddev/ddev-bridge-dist/host/<os>-<arch>/ddev-bridge uninstall   # or under ddev's global dir
```
It stops the bridge and removes the hook, `ddev bridge` and the binaries. It keeps your registry, rules
and log, and the sbx rule; [reference](docs/reference.md#files) lists them.

## Security
- The bridge listens on loopback only. The project comes from the token, never from the request.
- ddev runs via argv, no shell. Every call is in the audit log, with the rule that decided.
- Writes into the project go through Go's `os.Root`: a symlink leading out of the project makes them fail.
- Approved custom host commands (`.ddev/commands/host/`) run on the host with your SSH keys; the approval
  only covers their content. Deny the ones that reach remote servers.
- **The checks are argv checks, not a network boundary.** The web container still has internet access,
  and code the agent writes and then runs (scripts, composer scripts, tests, PHP run by drush) isn't
  inspected. The bridge guards against accidental and casual web access, not a determined agent.
- Don't keep SSH keys loaded in the web container (`ddev auth ssh`) during agent sessions.

More: [known limitations](docs/reference.md#known-limitations). Building and releasing:
[development](docs/development.md).
