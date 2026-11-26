# DDEV Bridge: Agent Guide

In a sandbox with `.ddev-bridge/bin/ddev-bridge-client`, you reach the host DDEV project only through this bridge. This overrides general DDEV guidance: no Docker, `ddev ssh`, SSH, or interactive and long-running sessions; run one-shot commands.

## Calling
- `ddev` and `drush` are already on PATH (symlinks in `.ddev-bridge/bin/`, set by the session-start hook); don't export it. Fallback: `ddev-bridge-client drush cache:rebuild`.
- Use full names (`drush cache:rebuild`, `drush config:import`), not aliases: ambiguous short forms are denied, and full names keep the audit log readable. Call tools directly (`ddev exec vendor/bin/phpunit`, not `ddev exec php vendor/bin/phpunit`): host rules name the tool.
- `ddev exec` runs one program with its arguments as your shell passes them; no shell runs in the container. Quote for your own shell only (`-resize '1280x1280>'` arrives intact); `>`, `|`, `&&` and `*` act in the sandbox.
- No stdin or TTY: pass `-y`. Run one bridge call at a time per project.

## Allowed and denied
- Allowed: everything local to the project: drush, composer, `ddev exec <program>`, start/stop/restart, logs, snapshots, database import/export, custom commands.
- Denied: the web (remote environments, site aliases, `pull`/`push`/`share`, `curl`/`wget`/`ssh`/`git clone`); the host or other projects (`poweroff`, `delete`, `list`, `add-on remove`, `--all`, other projects, paths outside the project); shells and inline code (`bash`, `php -r`, `drush php:eval`, `composer run-script`); programs that run other programs inside `ddev exec` (`timeout`, `time`, `env`, `nice`, `xargs`; put `timeout` or `time` in front instead: `timeout 60 ddev exec …`). The developer may add host rules (`denied by the developer's host policy`).
- Allowed isn't harmless: confirm with the user before writing data, config, dependencies or the environment (SQL other than `SELECT`/`SHOW`/`DESCRIBE`/`EXPLAIN`, `config:import`, `config:set`, `pm:uninstall`, `site:install`, `sql:drop`, `deploy`, `composer require`/`update`/`remove`, `import-db`, `snapshot restore`, `start`, `restart`, `stop --remove-data`), unless the task asked for it.
- Database reads: `drush sql:query "SELECT …"` or `ddev mysql -e "SELECT …"` with the query inline (several statements are fine) run without a prompt. Queries from stdin, a file or shell variables prompt.

## Denials
- A denied call exits with 126 and prints `ddev-bridge: denied: <reason>`. It is final, host policy included: don't retry with other spellings, option order, `--`, wrappers, or a script, test or PHP file that reaches the denied capability another way.
- Report it, give the user the exact command to run on the host, and report dependent checks as not run.
- Remote environments (stage, integration, live) are unreachable; ask the user for logs, dumps or probes.

## `.ddev/` changes
- After a `.ddev` change the bridge pauses the project until the developer runs `ddev bridge approve` on the host; tell the user which files changed. Never run `ddev bridge` yourself.
- Edit `.ddev/` (or run `ddev config`) only when the task needs it, and mention the approval.
