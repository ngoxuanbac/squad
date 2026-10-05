# Plugin sources, install/update CLI, preparation, publishing

Contents: Trust and the global switch · Source identifiers · Resolution order · Examples · CLI ·
Update semantics · `build` preparation · npm installs · Publish to npm · Private registry · Git ·
Choosing a channel

## Trust and the global switch

- `install`/`add` means "I trust this codebase, its dependencies and future updates". Server code
  and preparation commands run unsandboxed as the daemon user on the daemon host (the remote host
  with `--host`).
- **Settings → Plugins → Enable plugins** (root `pluginsEnabled` in daemon `config.json`; apply
  edits with `paseo reload --json`, no restart). A plugin runs only when the global switch **and**
  its own `enabled` are on. **An automated tool must read the current value and get explicit user
  permission before turning it on.**

## Source identifiers

Paste into Settings → Plugins → Plugin source, or pass to `paseo plugin install` (alias `add`).

| Source | Form | Example |
| --- | --- | --- |
| Host directory | Absolute (recommended) or relative path on the daemon host | `/srv/paseo/plugins/review` |
| GitHub | `github:owner/repo` or bare `owner/repo` | `github:acme/paseo-review` |
| Git | `git:<URL or SCP>`; prefix optional for URLs/SCP | `git:https://git.example.com/acme/review.git` |
| npm | `npm:<name>[@<version\|tag\|range>]`; prefix optional | `npm:@acme/paseo-review@^1.2.0` |
| Subdirectory | Append `:relative/plugin/path` to any source | `github:acme/monorepo:plugins/review` |

Details:

- Relative paths resolve against the **daemon's** working directory. The app doesn't expand `~`.
- Git URL schemes: `https://`, `http://`, `ssh://`, `git://`, `file://`. SCP: `user@host:path`.
  `file://` means Git acquisition, **not** directory install.
- npm names: lowercase `name` or `@scope/name`; components start with letter/digit then
  letters/digits/`.`/`_`/`-`. No selector ⇒ `latest`. Not accepted: npm aliases, tarball URLs,
  `file:` specs (use a directory or Git source). Quote args with spaces/comparators.
- Unscoped npm package with selector **and** subdirectory needs the prefix:
  `npm:review@1.2.0:nested` (without it, `review@1.2.0:nested` parses as SCP Git).
- `--ref <branch|tag|commit>` (Git only) selects initial content; default is remote HEAD. Install
  selectors never constrain later updates. Legacy `--path rel/path` = subdirectory suffix.
- `--id <runtime-id>` overrides the manifest ID (install the same source twice). An existing ID is
  rejected without touching the installed one.

## Resolution order

1. An existing directory on the daemon host matching the **whole** identifier wins (even with `:`).
2. Recognize `npm:`, `github:`, `git:` prefixes (`git://` is a URL scheme) before suffix parsing.
3. Recognize a final `:relative/path` only if no segment is empty, `.` or `..` (lone `.` = root);
   `/` or `\` separate segments (use `/`). URL ports and SCP separators stay in the source.
4. Without a prefix, an existing directory matching the remaining source wins.
5. Git URLs / SCP → Git; exact `owner/repo` → GitHub HTTPS. `github:` requires shorthand; `git:`
   accepts shorthand, URLs, SCP.
6. Remaining npm name + optional selector → host registry. Otherwise reject.

## Examples

```bash
paseo plugin install /srv/paseo/plugins/review
paseo plugin install github:acme/paseo-review
paseo plugin add owner/monorepo:plugins/workspace
paseo plugin install git:https://git.example.com:8443/acme/monorepo.git:plugins/review --ref main
paseo plugin install git@git.example.com:acme/review.git
paseo plugin install file:///srv/repos/monorepo:plugins/review
paseo plugin install npm:paseo-review@1.2.0
paseo plugin install npm:@acme/paseo-review@next
paseo plugin install 'npm:@acme/paseo-review@>=1.2.0 <2.0.0' --id review-staging
paseo plugin install npm:@acme/plugins@^1.2.0:plugins/review
```

## CLI

```bash
paseo plugin init /absolute/path/to/plugin
paseo plugin install <source> [--id <runtime-id>] [--ref <git-ref>]
paseo plugin ls [id]                      # status, error, source identity, installed revision (offline)
paseo plugin update <id> | --all [--check] [--yes]
paseo plugin update <id> --version <version|tag|range>   # npm explicit target, applies without prompt
paseo plugin update <id> --ref <branch|tag|commit>       # git explicit target
paseo plugin reload <id>                  # recompile + restart; required after source edits
paseo plugin logs <id> [--json]
paseo plugin enable|disable|remove <id>
paseo --host <url> plugin <command> ...   # target another daemon; --host goes BEFORE "plugin"
```

## Update semantics

- Shows current vs proposed revision (+ review links) and asks for approval; decline = no change.
- npm: offers only a newer `latest`; newer-than-latest installs stay. Explicit `--version` may downgrade.
- Git: offers remote default HEAD regardless of the install-time ref.
- Directory: skipped — edit and `reload`.
- `--check` previews only (wins even with `--yes`). `--all` reports per plugin; one failure doesn't
  stop others. Explicit targets can't combine with `--all`. JSON/noninteractive updates need `--yes`.
- Approval fetches exactly the reviewed commit/artifact; if it changed, the update fails — check again.
  Failed preparation/activation keeps the previous install.
- `remove` never deletes a directory source; it deletes managed files for Git/npm.

## `build` preparation

Most plugins omit it. Use only when the staged checkout must install a dependency Paseo doesn't
provide, generate code/assets, or do another required step.

```json
{ "id": "review", "requirements": { "paseo": ">=0.8.0" }, "build": [["npm", "ci", "--omit=dev"]] }
```

- List of non-empty argv arrays, executed directly (**no shell**), from the staged plugin dir, after
  resolving the exact commit and manifest. Never inferred from lockfiles.
- Runs on install **and** update, before validation/compile/activation. Failure → output reported,
  candidate discarded, running version intact. Commands are logged in the daemon log.
- npm lifecycle scripts never run (including dependencies'); native rebuilds need an explicit command.

## npm installs (daemon side)

- Node + npm must be on the **daemon host** `PATH`; the daemon uses that host user's npm config and
  auth (incl. scoped registries). The client never downloads or runs npm.
- Each candidate + production deps installs in an isolated dir; full tree and `package-lock.json`
  kept. Loading/enabling/reloading don't need npm.
- Any failure (download, deps, manifest, preparation, compile, activation) discards the candidate;
  other plugins keep running.

## Publish to npm

```bash
npm pkg set name=@acme/paseo-review version=1.0.0
npm pkg delete private
npm run typecheck
npm pack --dry-run          # verify assets (icon.svg etc.) are included
npm publish --access public
paseo plugin install npm:@acme/paseo-review@1.0.0   # test on a daemon host
```

- Scaffold `files`: `paseo-plugin.json`, `index.client.ts(x)`, `index.server.ts(x)`, `client/`,
  `server/`, `shared/`. Add assets outside these.
- SDK/host libraries stay in `devDependencies`; real runtime libs via `npm install <pkg>`.
- npm package name identifies the **source**; manifest `id` identifies the **installed plugin**.
- Paseo compiles TS — no build step needed. If you generate code/assets, generate before publish,
  keep generated JS in its runtime dir, include it in `files`, keep host modules external, and drop
  Git-only `npm ci` preparation from the published manifest.

## Private registry (GitHub Packages)

```bash
npm publish --registry=https://npm.pkg.github.com
```

On the daemon host, as the user running Paseo:

```ini
# ~/.npmrc
@acme:registry=https://npm.pkg.github.com
```

```bash
npm login --scope=@acme --auth-type=legacy --registry=https://npm.pkg.github.com  # PAT classic, read:packages
paseo plugin install npm:@acme/paseo-review
```

The app only takes the identifier; credentials live in host npm config.

## Git

Push the project; users run `paseo plugin install github:acme/paseo-review` or
`git:https://git.example.com/acme/paseo-review.git`. With runtime npm dependencies, commit
`package-lock.json` and add `"build": [["npm", "ci", "--omit=dev"]]` (npm needed on host).
Plugins using only host libraries need no preparation.

## Choosing a channel

| Situation | Channel |
| --- | --- |
| Local development, single machine | Directory (absolute path) + `reload` |
| Team/company, versioned releases, private | npm private registry (scoped) |
| Open source, fast iteration, monorepo | GitHub / Git (+ `:subdir`, `--ref` for pinning first install) |
| Public, semver-pinned consumers | Public npm |
