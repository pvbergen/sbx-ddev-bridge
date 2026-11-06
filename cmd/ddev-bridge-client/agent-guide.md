# DDEV Bridge: Agent Guide

Instructions for an agent that runs in a sandbox using the bridge.

## How to call it
- When `.ddev-bridge/bin/ddev-bridge-client` exists and you run in a sandbox, you reach the host DDEV project only through this bridge. This overrides any general DDEV guidance you have: direct DDEV, Docker, `ddev ssh` and SSH access to the project are unavailable, and there are no interactive or long-running container sessions. Run one-shot commands instead.
- Call `ddev` and `drush` through the symlinks in `.ddev-bridge/bin/` (`drush cr`, `ddev drush cr`, `ddev logs`), with `.ddev-bridge/bin` first on PATH, or pass the `ddev` subcommand to the client (`ddev-bridge-client drush cr`).
- Use full command names (`drush config:import`, `drush cache:rebuild`, `ddev describe`), not aliases or abbreviations. The bridge resolves short forms, but it denies ambiguous ones, and full names keep the audit log readable.
- Call tools directly (`ddev exec vendor/bin/phpunit`), not through an interpreter (`ddev exec php vendor/bin/phpunit`). The developer's rules name the tool, so the interpreter form may be denied where the direct call is allowed.
- There is no stdin or TTY. Pass `-y` to commands that ask for confirmation.
- Run one bridge call at a time; the bridge runs only one command per project at once.

## What is allowed
- Everything local to this project: any drush command, composer, `ddev exec <program>`, start/stop/restart, logs, snapshots, database import/export inside the project, and the project's custom commands.
- Denied are calls that reach the web (remote environments and site aliases, `pull`/`push`/`share`, network tools such as `curl`/`wget`/`ssh`/`git clone`), calls that affect the host or other projects (`poweroff`, `delete`, `list`, `add-on remove`, `--all`, other project names, paths outside the project), and shells or inline code (`bash`, `php -r`, `drush php:eval`, `composer run-script`). The developer can add own rules on the host, including for custom commands (`denied by the developer's host policy`).
- "Allowed" does not mean "harmless". Before commands that write data, config, dependencies or the environment (for example `sql:query` with anything but a read, `config:import`, `config:set`, `pm:uninstall`, `site:install`, `sql:drop`, `deploy`, `composer require`/`update`/`remove`, `import-db`, `snapshot restore`, `stop --remove-data` or `restart`), confirm with the user unless the task explicitly asked for that action.

## When a call is denied
- A denied call exits with code 126 and prints `ddev-bridge: denied: …` with the reason.
- Treat a denial as final, including one from the host policy. Do not retry with aliases, other spellings, option reordering, `--` separators, wrappers or other workarounds. Do not reach a denied capability another way, for example by writing a script, test, composer script or PHP file that fetches from the web, and running that.
- Report what was denied, give the user the exact command to run on the host, and report the dependent verification as not run.
- Remote environments (stage, integration, live) are not reachable from the sandbox, neither through drush site aliases nor through `ssh` from the container. Ask the user for remote logs, database dumps or probes instead.

## When `.ddev/` changed
- If the bridge says the `.ddev` configuration changed, it is paused for this project until the developer reviews and approves the change. Tell the user which files changed and that they need to run `ddev bridge approve` on the host. Never run `ddev bridge` yourself; it is denied from the sandbox.
- Edit `.ddev/` (including running `ddev config`) only when the task needs it, and mention that approval is required afterwards.
