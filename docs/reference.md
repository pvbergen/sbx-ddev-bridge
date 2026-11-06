# ddev-bridge reference

## What is denied
Everything else for the enabled project is allowed: any drush command (contrib too), composer (including
`install`/`require`/`update`), `ddev exec` of any program in any project service, start/stop/restart, logs
(also `-f`), snapshots, import/export-db, config, xdebug, and the project's custom commands.

| Category | Denied |
|---|---|
| **Reaches the web** | `ddev pull`, `push`, `share`, `tunnel`, `auth`, `self-upgrade`, `add-on get`/`list` (except `--installed`), `get`, `utility download-images`/`test`. Drush site aliases (`@prod` …, except `@self`/`@none`), `sql:sync`, `rsync`, `ssh`, `pm:security`, `locale:check/update`, `self-update`, `browse`. In `exec`: `curl`, `wget`, `ssh`, `scp`, `rsync`, `nc`, `socat`, `telnet`, `ftp`, … and network `git` subcommands (`clone`, `fetch`, `pull`, `push`, `remote`, `submodule`, …). DB clients with `-h/--host`. |
| **Host / other projects** | `poweroff`, `clean`, `delete`, `list`, `hostname`, `launch`, `mailpit`, host DB GUIs, `config global`, `--all`/`-a`, other project names as arguments, host paths outside the project (incl. via symlinks) for `import-db`/`export-db`/`import-files`, `add-on remove` (runs the add-on's removal actions on the host), `composer global`/`self-update`/`config -g`, `ddev bridge`. Unknown ddev subcommands (neither built-in nor a custom command). |
| **Shell / eval** | `ddev ssh`; in `exec`: `bash`, `sh`, `zsh`, …, wrappers that run other programs (`env`, `sudo`, `xargs`, `timeout`, `nohup`, `awk`, `find -exec`, …), `php -r/-a`, `python -c`, `node -e`, `perl -e`, `ruby -e`, `npx`/`npm exec`/`yarn dlx`, `git -c`. Drush `php:eval`, `php:script`, `php:cli`. `composer exec`/`run-script`. SQL shell escapes (`\!`, `system`, `\o |`, `COPY … PROGRAM`). |

The same rules apply to the ddev shortcuts (`ddev php`, `ddev mysql`, `ddev npm` …) and to indirect calls
(`ddev exec vendor/bin/drush …`, `ddev exec php vendor/bin/drush …`, `ddev exec composer …`).

**Drush names** are checked as drush resolves them, aliases and abbreviations included (`php:ev` is
`php:eval`). The bridge reads the project's commands with `ddev drush list --format=json` on the first drush
call and keeps them in memory; an unknown name lists them again (at most every 30 s). Ambiguous or unknown
names are denied, and so are drush calls while the list can't be read, e.g. when the project is stopped.
Options before the command must carry their value (`--uri=x`).

## `.ddev/` approval
ddev reads hooks (`exec-host` runs on the host), extra docker-compose files (which can mount any host
directory) and custom commands (including host commands) from `.ddev/`, and the agent can edit them. So the
bridge fingerprints `.ddev/` when the project is enabled and pauses it (every command is denied with the list
of changed files) as soon as it differs. A command that itself changes `.ddev/` says so when it ends.

**Checked:** everything in `.ddev/` except:
- ddev's generated dot-files (but `.env*` and `.global_commands` are checked);
- `db_snapshots/`, `import-db/`;
- `addon-metadata/`, except each add-on's `manifest.yaml`: `ddev add-on remove` runs the `removal_actions`
  stored there on the host;
- `#ddev-generated` files in the folders ddev regenerates for the containers (traefik, nginx, apache,
  web-build, …). Config, compose, host-command and mutagen files are checked, marker or not.

**Symlinks** in the checked part of `.ddev/` (or `.ddev` itself as a symlink) pause the project and can't be
approved: ddev follows them, so the target could change after approval. Replace them with regular files.

```bash
ddev bridge diff      # changes since the last approval (exit 0: none, 1: changes, 2: error)
git diff .ddev/       # review them
ddev bridge approve   # shows the diff again, asks, approves (-y: without asking)
```
After `ddev add-on get` or `remove` on the host, ddev writes the add-on manifest after the add-on's own
actions, so approve once. Installing or updating ddev-bridge doesn't touch `.ddev/`.

## Host policy
Your own rules live in `~/.config/ddev-bridge/policy` (macOS: `~/Library/Application Support/ddev-bridge/policy`).
The bridge reads the file on every request. Manage it with `ddev bridge policy` or edit it by hand:

```
# rules before the first [section] apply to every project
deny  drush sql:drop               # the reason after # is shown to the agent
allow ddev composer %

[my-project]                       # only this ddev project
deny  ddev get_dump                # ssh/rsync to the live server
```

- A rule matches the start of a command: `deny drush sql:drop` also covers `drush sql:drop -y`.
- `%` matches any text inside a word (`drush sql:%`), and a lone `%` at the end matches any further words
  (`drush %`, `ddev %`). Use `%`, not `*`: the shell expands `*`, and the file rejects it.
- Commands are normalized, so one rule covers every route: drush via `ddev drush`, `ddev exec` or
  `php vendor/bin/drush`, with names and aliases resolved (`cim` is `config:import`); composer is
  `ddev composer …`; other programs are `ddev exec <name> …`; ddev aliases are resolved (`ddev st` is
  `ddev describe`). Options are ignored; write option values attached (`--file=x`), since a separate value
  counts as a word.
- The **last** matching rule wins, and a project's section comes after the global rules. A matching rule
  decides **even over the built-in denials**: `allow drush %` also opens `php:eval` and remote site aliases.
  `ls`, `status` and the add command flag such rules, and the audit log records each overridden call.
  `ddev bridge` itself stays denied from the sandbox.
- Custom commands are allowed by default. Deny those that reach remote servers (`deny ddev get_dump`).
- An invalid file makes the bridge deny everything, with the parse error and line.

```bash
ddev bridge policy                                              # list, numbered in evaluation order
ddev bridge policy deny --reason "reaches live" ddev get_dump   # this project
ddev bridge policy deny --global drush sql:drop                 # every project
ddev bridge policy allow ddev exec phpunit
ddev bridge policy rm 2                                         # or `rm` to pick from the list
ddev bridge policy reset                                        # all rules of this project (asks first)
ddev bridge policy reset --global                               # all global rules
```
Flags come before the rule. Adding a rule with the same words replaces it in place.

## `ddev bridge` commands
On the host only; denied from the sandbox.

| Command | |
|---|---|
| `ddev bridge` / `status` | Running? Version, PID, log, registered projects |
| `ddev bridge enable` / `disable` | Enable this project for the agent / undo it |
| `ddev bridge diff` | `.ddev/` changes awaiting approval |
| `ddev bridge approve [-y]` | Show the diff, ask, approve |
| `ddev bridge ensure` / `stop` | Start in the background (if this project is enabled) / stop |
| `ddev bridge log [N]` | Last N lines of the bridge log |
| `ddev bridge policy [ls\|allow\|deny\|rm\|reset]` | Your own rules, see [Host policy](#host-policy) |
| `ddev bridge list` / `version` | Registered projects / version |
| `ddev bridge register` / `unregister` | Only the registry part of `enable` / `disable` |

The global `post-start` hook runs `ddev bridge ensure` on every `ddev start`; it does nothing in a project
that isn't enabled. A project with `skip_global_hooks: true` skips it; run `ddev bridge ensure` there.

## Exit codes
| Code | |
|---|---|
| `126` | Denied by the built-in rules or the host policy (reason on stderr) |
| `125` | Bridge unreachable, or a protocol error |
| `124` | Timed out (10 min by default; `ddev-bridge serve --timeout`) |
| `130` | Cancelled |
| other | The command's own exit code |

Output is capped at 8 MiB per stream, and one command runs per project at a time.

## Files
| | Linux / WSL2 | macOS |
|---|---|---|
| Binaries, `ddev bridge` | ddev's global dir: `ddev-bridge-dist/`, `commands/host/bridge` | same |
| Global hook | `global_config.yaml` in ddev's global dir | same |
| Registry (token hashes, `.ddev/` fingerprints) | `~/.config/ddev-bridge/projects.json` | `~/Library/Application Support/ddev-bridge/projects.json` |
| Host policy | `~/.config/ddev-bridge/policy` | `~/Library/Application Support/ddev-bridge/policy` |
| Log (rotated at 10 MB), PID | `~/.local/state/ddev-bridge/` | same |
| Per project | `.ddev-bridge/` (client, `ddev`/`drush` links, token), one `.gitignore` line, `.claude/settings.local.json` overlay | same |

ddev's global dir is `~/.ddev` by default (`ddev version` shows it). `uninstall` removes the first two rows;
to remove the rest: `rm -rf ~/.config/ddev-bridge ~/.local/state/ddev-bridge` (macOS: the
`Application Support` folder instead of `~/.config`) and `sbx policy rm network --resource localhost:7474`.

Environment overrides:
- Sandbox: `DDEV_BRIDGE_URL` (default `http://host.docker.internal:7474`), `DDEV_BRIDGE_TOKEN`.
- Host: `DDEV_BRIDGE_CONFIG_DIR` (registry, policy), `DDEV_BRIDGE_STATE_DIR` (log, PID). ddev's global dir
  follows ddev (`DDEV_XDG_CONFIG_HOME`); `ddev-bridge setup|uninstall --global-dir` overrides it.

## Audit log
`ddev bridge log` shows startup lines and one JSON line per request:

| Field | |
|---|---|
| `time`, `remote` | When, and the client address |
| `project` | Project name (from the token) |
| `argv` | The command as requested |
| `ran` | The command as run, when the bridge rewrote it (e.g. `exec --raw`) |
| `outcome` | `ran`, `denied`, `cancelled`, `unauthorized`, `bad_request` or `error` |
| `reason` | Why it was denied or failed |
| `host_rule` | The host policy rule that decided (`file:line: rule`) |
| `override` | The built-in denial a host `allow` rule lifted |
| `ddev_changed` | `.ddev/` files the command changed (the project is now paused) |
| `exit_code`, `duration` | Result |

## Known limitations
- **Cancel/timeout** kills the host-side process group (`ddev` → `docker exec`). A process already running
  inside the container (e.g. a long phpunit run) may keep running there until it finishes, because
  `docker exec` doesn't forward the kill.
- Streaming goes through the sandbox proxy. If output arrives in bursts, the proxy is buffering it.
- No stdin or TTY. Pass `-y` to drush commands that ask for confirmation.
- The `.ddev/` check runs before each command; an edit racing with a running command is caught by the next one.
- The checks are argv checks, not a network boundary; see [Security](../README.md#security).
