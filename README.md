# ddev-bridge

Lets an AI agent running in a Docker sandbox (`sbx`) use ddev **freely for its own project** on the host,
without moving ddev into the sandbox. The bridge refuses what would make ddev **reach the web**, **touch
the host or other projects**, or **open a shell / evaluate inline code**.
Packaged as a ddev add-on. Host: Linux, WSL2 (verified first) and macOS.

```
sandbox: ddev / drush (client shim) ──HTTP──► host.docker.internal:7474
host:    ddev-bridge serve ─► token → project root ─► .ddev/ approved? ─► policy ─► ddev <argv> (no shell, cwd = root)
                                                     ◄── live stdout/stderr + exit code (NDJSON stream)
```

## What is denied
Everything else for the registered project is allowed: any drush command (contrib too), composer
(including `install`/`require`/`update`), `ddev exec` of any program in any project service, start/stop/
restart, logs (also `-f`), snapshots, import/export-db, config, xdebug, and the project's custom commands.

| Category | Denied |
|---|---|
| **Reaches the web** | `ddev pull`, `push`, `share`, `tunnel`, `auth`, `self-upgrade`, `add-on get`/`list` (except `--installed`), `get`, `utility download-images`/`test`. Drush site aliases (`@prod` …, except `@self`/`@none`), `sql:sync`, `rsync`, `ssh`, `pm:security`, `locale:check/update`, `self-update`, `browse`. In `exec`: `curl`, `wget`, `ssh`, `scp`, `rsync`, `nc`, `socat`, `telnet`, `ftp`, … and network `git` subcommands (`clone`, `fetch`, `pull`, `push`, `remote`, `submodule`, …). DB clients with `-h/--host`. |
| **Host / other projects** | `poweroff`, `clean`, `delete`, `list`, `hostname`, `launch`, `mailpit`, host DB GUIs, `config global`, `--all`/`-a`, other project names as arguments, host paths outside the project (incl. via symlinks) for `import-db`/`export-db`/`import-files`, `add-on remove` (runs the add-on's removal actions on the host), `composer global`/`self-update`/`config -g`. Unknown ddev subcommands (neither built-in nor a custom command). |
| **Shell / eval** | `ddev ssh`; in `exec`: `bash`, `sh`, `zsh`, …, wrappers that run other programs (`env`, `sudo`, `xargs`, `timeout`, `nohup`, `awk`, `find -exec`, …), `php -r/-a`, `python -c`, `node -e`, `perl -e`, `ruby -e`, `npx`/`npm exec`/`yarn dlx`, `git -c`. Drush `php:eval`, `php:script`, `php:cli`. `composer exec`/`run-script`. SQL shell escapes (`\!`, `system`, `\o |`, `COPY … PROGRAM`). |

The same tool rules apply to the ddev shortcuts (`ddev php`, `ddev mysql`, `ddev npm` …) and to
indirect calls (`ddev exec vendor/bin/drush …`, `ddev exec php vendor/bin/drush …`, `ddev exec composer …`).

Drush command names are checked as drush resolves them, aliases and abbreviations included
(`php:ev` is `php:eval`). The bridge reads the project's commands with `ddev drush list --format=json`
on the first drush call and keeps them in memory; an unknown name lists them again (at most every 30 s).
Ambiguous or unknown names are denied, and so are drush calls while the list can't be read, e.g.
when the project is stopped. Options before the command must carry their value (`--uri=x`).

### `.ddev/` approval
ddev reads hooks (`exec-host` runs **on the host**), extra docker-compose files (which can mount any
host directory) and custom commands (including host commands) from `.ddev/`, and the agent can edit
them. So the bridge fingerprints `.ddev/` when you register the project and **pauses** (every command
is denied with the list of changed files) as soon as it differs. Ignored: ddev's generated dot-files (but not `.env*`), `db_snapshots/`, `addon-metadata/` except each add-on's `manifest.yaml`, and
`#ddev-generated` files in folders ddev regenerates for the containers (traefik, nginx, apache,
web-build, …). Config, compose, host-command and mutagen files are always checked, marker or not.
Add-on manifests are checked because `ddev add-on remove` runs the `removal_actions` stored in them on the host.
Symlinks in the checked part of `.ddev/` (or `.ddev` itself as a symlink) pause the bridge and can't be approved:
ddev follows them, so the agent could change the target after approval. Replace them with regular files.

```bash
ddev bridge diff      # what changed since the last approval (exit 1 if anything did, 2 on error)
git diff .ddev/       # review it
ddev bridge approve   # shows the diff again, asks, and accepts the current state
```
This also applies after the agent runs `ddev config …`, and after every `ddev add-on get` or `remove` on the
host, including installing this add-on: ddev writes the add-on manifest after the install actions, so approve once.

### Host policy (your own rules)
Your own allow and deny rules live in a plain-text file next to the registry
(`~/.config/ddev-bridge/policy`; on macOS `~/Library/Application Support/ddev-bridge/policy`).
The bridge reads it on every request. Manage it with `ddev bridge policy` or edit it by hand:

```
# rules before the first [section] apply to every project
deny  drush sql:drop               # the reason after # is shown to the agent
allow ddev composer %

[orell-fuessli]                    # only this ddev project
deny  ddev get_dump                # ssh/rsync to the live server
```

- A rule matches the start of a command: `deny drush sql:drop` also covers `drush sql:drop -y`.
- `%` matches any text inside a word (`drush sql:%`), and a lone `%` at the end matches any further
  words (`drush %`, `ddev %`). Use `%`, not `*`: the shell expands `*`, and the file rejects it.
- Commands are normalized, so one rule covers every route: `drush` via `ddev drush`, `ddev exec` or
  `php vendor/bin/drush`, with names and aliases resolved (`cim` is `config:import`); composer is
  `ddev composer …`; other programs are `ddev exec <name> …`; ddev aliases are resolved (`ddev st`
  is `ddev describe`). Options are ignored; write option values attached (`--file=x`), since a
  separate value counts as a word.
- The **last** matching rule wins, and a project's section comes after the global rules. A matching
  rule decides **even over the built-in denials**: `allow drush %` also opens `php:eval` and remote
  site aliases. `ls`, `status` and the add command flag such rules, and the audit log records each
  overridden call (`host_rule`, `override`). `ddev bridge` itself stays denied from the sandbox.
- Custom commands are allowed by default. Deny those that reach remote servers (`deny ddev get_dump`).
- An invalid file makes the bridge deny everything, with the parse error and line.

```bash
ddev bridge policy                                          # list, numbered in evaluation order
ddev bridge policy deny --reason "reaches live" ddev get_dump   # this project
ddev bridge policy deny --global drush sql:drop             # every project
ddev bridge policy allow ddev exec phpunit
ddev bridge policy rm 2                                     # or `rm` to pick from the list
ddev bridge policy reset                                    # all rules of this project (asks first)
ddev bridge policy reset --global                           # all global rules
```
Flags come before the rule. Adding a rule with the same words replaces it in place.

Exit codes:
- `126`: denied by the built-in or the host policy (reason on stderr)
- `125`: bridge unreachable or a protocol error
- `124`: timed out (10 min by default)
- `130`: cancelled
- anything else: the command's own exit code

Output is capped at 8 MiB per stream, and only one command runs per project at a time.

## Install (ddev add-on)
Once per project, on the host (Linux, WSL2 or macOS). The add-on ships as a release tarball that
contains the prebuilt binaries (the repository itself has no binaries):
```bash
cd ~/projects/my-d11-site
# latest release (private repo, so download it with gh first)
gh release download -R pvbergen/sbx-ddev-bridge -p ddev-bridge.tar.gz -D /tmp --clobber
ddev add-on get /tmp/ddev-bridge.tar.gz
ddev restart
```
Other sources:
- A specific version: add the tag, e.g. `gh release download v2.1.0 -R pvbergen/sbx-ddev-bridge …`.
- A local clone: `make release` there, then `ddev add-on get /path/to/sbx-ddev-bridge`.
- `ddev add-on get pvbergen/sbx-ddev-bridge` does **not** work: ddev fetches the source tarball,
  which has no binaries.

The add-on:
- puts the binaries in ddev's global dir (`~/.ddev/ddev-bridge-dist/`) and adds the host command
  `ddev bridge`. Nothing binary lands in the project's `.ddev/`.
- installs the sandbox client as `.ddev-bridge/bin/{ddev-bridge-client,ddev,drush}`, matching your CPU,
  and adds `/.ddev-bridge/` to `.gitignore`, together with the add-on's `.ddev/config.ddev-bridge.yaml`
  and `.ddev/addon-metadata/ddev-bridge/`. The bridge is a per-developer install: a committed config
  would run `ddev bridge ensure` on every teammate's `ddev start`, which fails without the add-on. If
  these files are already tracked, untrack them with `git rm --cached`.
- registers the project (token in `.ddev-bridge/token`) and approves the current `.ddev/`,
  including its own `.ddev/config.ddev-bridge.yaml`.
- adds a `post-start` hook, so every `ddev start` runs `ddev bridge ensure`. That starts the bridge
  in the background unless the same version already runs, and replaces an older version.
- runs `sbx policy allow network localhost:7474` if `sbx` is installed, or prints the command.
- adds a Claude Code overlay to `.claude/settings.local.json` (see [Agents](#agents)), keeping
  everything else in the file. If the file isn't valid JSON, it is left alone and the snippet is printed.

Updating is the same download and `ddev add-on get` again. Removing: see [Uninstall](#uninstall).

The sandbox must be in **direct mode**. In clone mode the client refuses to run, because host ddev
wouldn't see the agent's edits.

### Agents
**Claude Code** needs no setup. The add-on's overlay in `.claude/settings.local.json` has:
- a `SessionStart` hook, `ddev-bridge-client --session-start`. On the host it does nothing. In a sandbox
  it puts `.ddev-bridge/bin` on PATH (via `$CLAUDE_ENV_FILE`) and gives the agent the
  [agent guide](cmd/ddev-bridge-client/agent-guide.md) and whether the bridge is reachable;
- `permissions.ask` rules for drush and ddev commands that change data, config, dependencies or the
  environment (`drush cim`, `ddev composer require`, `ddev restart`, …). These prompts also apply to
  host sessions in the project.

**Other agents:** put `.ddev-bridge/bin` first on PATH, for example from a session hook or shell profile,
and give the agent the [agent guide](cmd/ddev-bridge-client/agent-guide.md) as instructions.

### Uninstall
**From one repository** (on the host):
```bash
ddev add-on remove ddev-bridge
ddev restart
```
This unregisters the project and removes `.ddev-bridge/` (client, symlinks, token), the ddev-bridge lines
in `.gitignore`, the Claude Code overlay in `.claude/settings.local.json`, `.ddev/config.ddev-bridge.yaml`
and `.ddev/addon-metadata/ddev-bridge/`. The binaries in
the global dir stay for other projects. If the global binary is already gone, the removal prints what to do
by hand: delete those files and the `.gitignore` block yourself, and run `ddev bridge unregister` if the
command still exists (otherwise drop the project from `~/.config/ddev-bridge/projects.json`).

**From the machine** (after removing it from every repository; a repository that still has the add-on
gets a failing `post-start` hook):
```bash
ddev bridge stop
rm -rf ~/.ddev/ddev-bridge-dist ~/.ddev/commands/host/bridge   # or under $DDEV_GLOBAL_DIR / ~/.config/ddev
rm -rf ~/.config/ddev-bridge ~/.local/state/ddev-bridge        # registry, log, PID
sbx policy rm network --resource localhost:7474
```

### `ddev bridge` (host only; denied from the sandbox)
| Command | |
|---|---|
| `ddev bridge` / `status` | Running? Which version, PID, log, registered projects |
| `ddev bridge diff` | `.ddev/` changes awaiting approval (exit 1 if any, 2 on error) |
| `ddev bridge approve [-y]` | Show the diff, ask, approve |
| `ddev bridge ensure` / `stop` | Start in the background / stop |
| `ddev bridge log [N]` | Last N lines of the bridge log (startup + JSONL audit) |
| `ddev bridge policy [ls\|allow\|deny\|rm\|reset]` | Your own rules, see [Host policy](#host-policy-your-own-rules) |
| `ddev bridge register` / `unregister` / `list` / `version` | As the names say |

State: `~/.local/state/ddev-bridge/` (`bridge.log`, rotated at 10 MB; `bridge.pid`). Registry:
`~/.config/ddev-bridge/projects.json` (token hashes and the `.ddev/` fingerprint only), host policy:
`~/.config/ddev-bridge/policy`.

Projects registered with an earlier bridge must be approved once: `ddev bridge approve`.

## Development
```bash
make            # vet + tests, dev builds in dist/
make release    # add-on binaries in ddev-bridge-dist/ (VERSION=… optional)
make package    # release + dist/ddev-bridge.tar.gz, the installable add-on
make clean      # remove all build output
```
Needs Go 1.25 or newer (`os.Root`); the binaries are static, the macOS ones need macOS 12 or newer.
Build output (`dist/`, `ddev-bridge-dist/`) is gitignored. The binaries embed ddev's
`#ddev-generated` signature, so `ddev add-on get` replaces them on update.

### CI and releases
- `.github/workflows/ci.yml`: on every PR and push to `main`, runs the tests and a test build of
  the package (`make package`). Nothing is uploaded.
- `.github/workflows/release.yml`: when a GitHub release is published, checks out its tag, runs the
  tests, builds the package with that version, checks the binaries' version and marker, and attaches
  `ddev-bridge.tar.gz` and its `.sha256` to the release.

To release: on GitHub, Releases → Draft a new release, create a tag like `v2.1.0` (`vX.Y.Z`, optionally
`-rc1`; mark those as pre-release), publish. Or: `gh release create v2.1.0 --generate-notes`. The
tarball appears on the release a few minutes later.

Manual setup without the add-on:
```bash
ddev-bridge install-project --client dist/linux-amd64/ddev-bridge-client ~/projects/my-d11-site
ddev-bridge serve             # foreground; or `ddev-bridge ensure` for the background
```

Environment overrides:
- In the sandbox: `DDEV_BRIDGE_URL` (default `http://host.docker.internal:7474`) and `DDEV_BRIDGE_TOKEN`.
- On the host: `DDEV_BRIDGE_CONFIG_DIR`, `DDEV_BRIDGE_STATE_DIR`, `DDEV_GLOBAL_DIR`.

## Known limitations
- **Cancel/timeout** kills the host-side process group (`ddev` → `docker exec`). A process already
  running *inside* the container (e.g. a long phpunit run) may keep running there until it finishes,
  because `docker exec` doesn't forward the kill.
- Streaming goes through the sandbox proxy. If output arrives in bursts instead of live, the proxy is
  buffering the response.
- No stdin/TTY. Pass `-y` to drush commands that ask for confirmation.

## Security notes
- The bridge binds to loopback only. The project directory comes from the token, never from the request.
- `ddev` is executed via argv with no shell, and `exec` is forced to `--raw`.
- The audit log records both the requested argv and the argv actually run, and the host rule that decided.
- Install and uninstall write into the project (client, token, `.gitignore`, `.claude/settings.local.json`)
  only through Go's `os.Root`: a symlink the agent planted to lead out of the project (e.g.
  `.ddev-bridge/bin → ~/bin`) makes the install fail instead of writing on the host.
- Custom host commands (`.ddev/commands/host/`) run on the host with your SSH keys. The `.ddev/` approval
  only covers their content; deny the ones that reach remote servers in the host policy.
- **The checks are argv checks, not a network boundary.** The web container still has internet access.
  Code the agent writes to disk and then runs is not inspected: a script (`ddev exec ./x.sh`), composer
  scripts during `install`/`update`, phpunit bootstraps, PHP executed by drush, git hooks, cron fetching
  update/locale data. The shell/eval denials only stop one-liners. Treat the bridge as a guard against
  accidental and casual web access, not against a determined agent. A container egress firewall is
  planned for later.
- The `.ddev/` check runs before each command; an edit racing with a running command is not detected
  until the next one.
- The web container may hold keys from `ddev auth ssh`. Don't keep keys loaded during agent sessions.
