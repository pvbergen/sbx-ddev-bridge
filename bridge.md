---
helga-id: "runtime.ddev-bridge"
helga-load: "conditional"
helga-when: "Running ddev, drush, composer, cache clears, logs or code checks from an agent sandbox"
helga-paths: ".ddev-bridge/bin/**"
helga-requires: "runtime.ddev"
---

# DDEV Bridge (Agent Sandbox)

- When `.ddev-bridge/bin/ddev-bridge-client` exists, the agent runs in a sandbox that reaches the host DDEV project only through this bridge. This narrows `runtime.ddev`: direct DDEV, Docker and SSH access to the project are unavailable.
- Call the bridge through the `ddev` and `drush` symlinks in `.ddev-bridge/bin/`, which dispatch on their name (`drush cr`, `ddev logs`), or pass the `ddev` subcommand to the client (`ddev-bridge-client drush cr`).
- Everything local to this project is allowed: any drush command, composer, `ddev exec <program>`, start/stop/restart, logs, snapshots, database import/export inside the project, and the project's custom commands.
- The bridge denies three kinds of calls: those that reach the web (remote environments and site aliases, `pull`/`push`/`share`, network tools such as `curl`/`wget`/`ssh`/`git clone`), those that affect the host or other projects (`poweroff`, `delete`, `list`, `add-on remove`, `--all`, other project names, paths outside the project), and shells or inline code (`bash`, `php -r`, `drush php:eval`, `composer run-script`). A refused call exits with code 126 and prints `ddev-bridge: denied: …` with the reason.
- Treat a denial as final. Do not retry with aliases, other spellings, option reordering, `--` separators, wrappers or other workarounds. Do not reach a denied capability another way, for example by writing a script, test, composer script or PHP file that fetches from the web, and running that. Report what was denied.
- Remote environments (stage, integration, live) are not reachable from the sandbox. Ask the user for remote logs, database dumps or probes instead.
- If the bridge says the `.ddev` configuration changed, it is paused for this project until the developer reviews the change and approves it. Tell the user which files changed and that they need to run `ddev bridge approve` on the host. Never try to run `ddev bridge` yourself; it is denied from the sandbox. Edit `.ddev/` (including running `ddev config`) only when the task needs it, and mention that approval is required afterwards.
- For anything the bridge refuses, give the user the exact command to run on the host and report the dependent verification as not run.
- "Allowed" does not mean "harmless". Before running commands that write data, config, dependencies or the environment (for example `sql:query` with anything but a read, `config:import`, `config:set`, `pm:uninstall`, `site:install`, `sql:drop`, `deploy`, `composer require`/`update`/`remove`, `import-db`, `snapshot restore`, `stop --remove-data` or `restart`), confirm with the user unless the task explicitly asked for that action.
- Run one bridge call at a time, the same as any other DDEV command.
