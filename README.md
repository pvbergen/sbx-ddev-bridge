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

### `.ddev/` approval
ddev reads hooks (`exec-host` runs **on the host**), extra docker-compose files (which can mount any
host directory) and custom commands (including host commands) from `.ddev/`, and the agent can edit
them. So the bridge fingerprints `.ddev/` when you register the project and **pauses** (every command
is denied with the list of changed files) as soon as it differs. Ignored: ddev's generated dot-files (but not `.env*`), `db_snapshots/`, `addon-metadata/` except each add-on's `manifest.yaml`, and
`#ddev-generated` files in folders ddev regenerates for the containers (traefik, nginx, apache,
web-build, …). Config, compose, host-command and mutagen files are always checked, marker or not.
Add-on manifests are checked because `ddev add-on remove` runs the `removal_actions` stored in them on the host.

```bash
ddev bridge diff      # what changed since the last approval (exit 1 if anything did)
git diff .ddev/       # review it
ddev bridge approve   # shows the diff again, asks, and accepts the current state
```
This also applies after the agent runs `ddev config …`, and after every `ddev add-on get` or `remove` on the
host, including installing this add-on: ddev writes the add-on manifest after the install actions, so approve once.

Exit codes:
- `126`: denied by policy (reason on stderr)
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

Updating is the same download and `ddev add-on get` again. Removing: `ddev add-on remove ddev-bridge` (the
binaries stay in the global dir for other projects).

In the sandbox, put `.ddev-bridge/bin` first on PATH, for example from your agent's session hook or
shell profile, and give the agent [`agent-guide.md`](agent-guide.md) as instructions. The sandbox must be
in **direct mode**. In clone mode the client refuses to run, because host ddev wouldn't see the agent's edits.

### `ddev bridge` (host only; denied from the sandbox)
| Command | |
|---|---|
| `ddev bridge` / `status` | Running? Which version, PID, log, registered projects |
| `ddev bridge diff` | `.ddev/` changes awaiting approval (exit 1 if any) |
| `ddev bridge approve [-y]` | Show the diff, ask, approve |
| `ddev bridge ensure` / `stop` | Start in the background / stop |
| `ddev bridge log [N]` | Last N lines of the bridge log (startup + JSONL audit) |
| `ddev bridge register` / `unregister` / `list` / `version` | As the names say |

State: `~/.local/state/ddev-bridge/` (`bridge.log`, rotated at 10 MB; `bridge.pid`). Registry:
`~/.config/ddev-bridge/projects.json` (token hashes and the `.ddev/` fingerprint only).

Projects registered with an earlier bridge must be approved once: `ddev bridge approve`.

## Development
```bash
make            # vet + tests, dev builds in dist/
make release    # add-on binaries in ddev-bridge-dist/ (VERSION=… optional)
make package    # release + dist/ddev-bridge.tar.gz, the installable add-on
make clean      # remove all build output
```
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
- The audit log records both the requested argv and the argv actually run.
- **The checks are argv checks, not a network boundary.** The web container still has internet access.
  Code the agent writes to disk and then runs is not inspected: a script (`ddev exec ./x.sh`), composer
  scripts during `install`/`update`, phpunit bootstraps, PHP executed by drush, git hooks, cron fetching
  update/locale data. The shell/eval denials only stop one-liners. Treat the bridge as a guard against
  accidental and casual web access, not against a determined agent. A container egress firewall is
  planned for later.
- The `.ddev/` check runs before each command; an edit racing with a running command is not detected
  until the next one.
- The web container may hold keys from `ddev auth ssh`. Don't keep keys loaded during agent sessions.
