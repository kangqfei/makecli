# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.5.14] - 2026-09-16

### Features

- **output**: `--output` now defaults to `auto`, which renders a table when stdout is a terminal and JSON when the command is piped, run in CI, or invoked by an agent. The same command serves both audiences without either having to remember the flag; explicit `--output table` / `--output json` still pin the format regardless of environment. `app deploy` without `--status` falls back to table under `auto` since JSON is only valid for status queries, so agents running a deploy are never stopped by the `--status` requirement
- **trace**: New hidden `trace` subcommand opens the OpenObserve trace page for a given `--id` in the browser, with `--days` / `--hours` / `--minutes` selecting the search window

### Tests

- **notifier**: Cases asserting a pending upgrade clear the `CI` environment variable, fixing a spurious red run on GitHub Actions where `CI=true` short-circuits the decision chain

## [v0.5.13] - 2026-09-11

### Features

- **notifier**: `--output json` now carries a pending upgrade as a `_notice.update` object (`current` / `latest` / `url` / `command` / `message`) appended to the top-level JSON object, mirroring lark-cli, so an agent running makecli in a non-TTY harness learns about a new release without reading stderr. The object is added by a byte-level append at the single JSON exit — existing fields and their order are untouched, array-shaped outputs are left as is, and output is byte-identical to before when no update is pending
- **notifier**: The stderr upgrade notice follows gh: it is printed only after a successful command and after all other output, never on failure, and the release URL line is gone (the URL stays in the JSON notice). The update decision is computed once at startup from the local cache into a process-level snapshot that both the stderr notice and the JSON exit read, so TTY detection is the only difference between the two channels

## [v0.5.12] - 2026-09-10

### Features

- **auth**: New global `--access-token` / `-t` flag and `$MAKE_ACCESS_TOKEN` environment variable override the token stored in the credentials file, resolved as flag > env > credentials through a single entry point shared by `configure verify`, `whoami`, and every authenticated command. The override replaces only the credential itself — tenant, operator, and server URLs still come from the `--profile` config. When the token comes from a flag or env, an auth failure no longer triggers an automatic re-login (the stored token would be shadowed anyway), `configure verify` reports a `source` field (`flag` / `env` / `credentials`) in JSON, and the auth-failure hint names where the token came from (#40)
- **config**: `$MAKE_META_SERVER_URL` and `$MAKE_REPO_SERVER_URL` join the host-address resolution chain, so all three override flags share the same configurable sources: flag > env > profile config > built-in environment preset. `configure resolve` and `configure verify` use the same chain instead of a hand-written copy (#40)

### Documentation

- **help**: Global URL flag hints mirror `--access-token`, naming both overridden sources as `(overrides $ENV and the profile <file>)` (#40)
- **skills**: `skills read` prints a blank line before its stderr navigation hint so it stays visually separate from the content

## [v0.5.11] - 2026-09-09

### Features

- **skills**: New `makecli skills read <skill>[/<path>] [path]` prints skill content embedded in the binary — the skill's `SKILL.md` by default, any file under `references/`, or a directory listing. The content is compiled in at build time from the `make-platform-skills` git submodule, so it always matches the CLI version and works offline; error messages carry the list of embedded skills or the skill's top-level entries so an agent can navigate without a separate `list` verb. Machine resources (`scripts/`) are not embedded
- **help**: The root `--help` ends with a one-time skills setup hint for humans (`makecli skills install --all --yes`), mirroring lark-cli

### Documentation

- **help**: Examples and hints consistently use long options instead of mixing `-y` with `--all` on one line
- **cmd**: `CLAUDE.md` registers `root_help_test.go` and `installUsageTemplate`

## [v0.5.10] - 2026-09-07

### Features

- **npm**: makecli is now distributed on npm as `@qfeius/makecli` — `npm install -g @qfeius/makecli` on macOS / Linux / Windows (x64 and arm64). The main package pins six platform sub-packages via `optionalDependencies`; npm installs only the matching one, with no download or postinstall script. `makecli update` detects npm and pnpm installs and delegates to `npm install -g` / `pnpm add -g` instead of replacing the binary in place

### Bug Fixes

- **npm**: The main package's launcher script was excluded from the repository by the bare `bin` rule in `.gitignore`, and the publish loop swallowed the resulting build failure, so the first beta shipped platform packages without the main package. Only the root `/bin/` is ignored now, and the packaging script emits nothing unless every package was written

### CI

- **release**: npm publishing switched to Trusted Publishing (OIDC): the workflow authenticates with its GitHub Actions identity instead of a long-lived `NPM_TOKEN`, and every package carries a signed provenance attestation

### Documentation

- **readme**: Installation lists npm before Homebrew

## [v0.5.9] - 2026-09-03

### Bug Fixes

- **update**: `makecli update` on Windows always failed with `gzip: invalid header` — the downloaded archive was saved to an extension-less temp file, so the `.zip` check on the path never matched and the zip was fed to the gzip decoder. Archive format is now decided by the release asset name; a download→extract regression test covers the full path

## [v0.5.8] - 2026-09-01

### Features

- **configure**: `configure verify` now decodes the token's JWT claims locally and fails closed on expiry — an expired token is reported invalid before any network call, so a lenient server endpoint returning 200 can no longer disguise an expired credential as valid. The message carries a ready-to-run `makecli login --profile <name>` hint, and the JSON output gains full `issued_at` / `expires_at` timestamps (RFC3339). Tokens without an `exp` claim still fall through to the online check

### Bug Fixes

- **configure**: `configure verify` built its own API client and dropped the global `--debug` flag, so no curl debug output was printed; it now injects the debug option like every other command
- **api**: Defer decoding of the `data` field on write paths, fixing a decode failure in `app delete` (#39)

### Documentation

- Fix docs

## [v0.5.7] - 2026-08-03

### ⚠ Breaking Changes

- **daemon**: The device face is now the *runtime* face. The wire protocol collapsed onto a single `ResourceRuntime` resource — enrollment goes through `CreateResource` with the setup key authenticating itself in the body (no bearer), heartbeats through `UpdateResource` with the node key as bearer — and the `Device*` wire types were renamed to match. Enrollment also moved up to construction: `--setup-key` is only consumed when no local node key exists and is exchanged for a long-lived node key that is merged into `~/.make/credentials` next to the `login` token, so a restart reconnects with the stored key and `--setup-key` can be dropped (#30, #34)

### Features

- **app**: `makecli app deploy` can now report and wait on build progress. `--status` looks up the build task for the current HEAD commit — the commit sha *is* the lookup key, so there is no task id to copy and a re-run simply re-attaches to the same build — and `--wait` blocks until the build reaches a terminal state, printing one line per status/phase transition (`RECEIVE → CLONE → DETECT → BUILD → PUSH → DEPLOY`) rather than redrawing. Exit codes carry the verdict so CI and agents never parse output text: `0` success, `2` build FAILED or CANCELED, `124` timeout (`--timeout`, default `5m`). Right after a push the task may not exist yet — that webhook window is tolerated instead of reported as failure. On success the environment URL is printed, and `--output json` (with `--status`) puts a single machine-readable object on stdout while progress goes to stderr (#38)
- **app**: New `makecli app info <appKey>` — app metadata plus a per-environment deployment table (status, commit, URL) for `preview` and `production`, with `--output table|json` (#38)
- **skills**: `skills install` and `skills uninstall` take explicit names or `--all`, and both run a plan → confirm → execute flow: the plan validates against the lockfile and gates on the npx environment up front, the confirmation can be skipped with `-y` and non-interactive shells are refused without it, and `--all` is expanded locally rather than passed through to the upstream tool. `skills list` now shows only installed skills by default, with `--all` for the remote catalog (#36)
- **update**: Release channels. A global `[settings] channel` (`stable` | `beta`) decides what `makecli update` and the background update notifier treat as an upgrade — `stable` follows GitHub's latest release, `beta` takes the highest semver across the whole release list, so a newer stable release automatically wins beta users back. Switch with `makecli configure set channel beta` (#23)
- **agent**: New hidden `makecli agent` — a keyless local code agent whose model traffic goes through the Make platform gateway, so the device needs no vendor API key. It ships a seven-tool registry (read / write / edit / grep / find / ls / bash) rooted at the working directory with per-directory trust confirmation for side-effecting tools (`--approve` pre-grants it), a one-shot `-p` mode and an interactive REPL in which `!<cmd>` runs a local shell command directly — no LLM round-trip, transcribed into history like Claude Code does. `--chat-only` (renamed from `--no-tools`) falls back to plain chat (#27, #29, #35, #37)
- **daemon**: `makecli daemon` now runs in the background by default — it writes a user LaunchAgent, hands the loop to launchd and returns to the prompt immediately, the way `netbird` behaves once its service is installed. Pass `--foreground` to block in the current terminal instead (that is exactly the form launchd starts, since a launchd job must not fork itself); on non-macOS the default falls back to the foreground with a note rather than failing. The lifecycle verbs are `makecli daemon start / stop / restart / status / uninstall` — macOS only, backed by that same user LaunchAgent (`~/Library/LaunchAgents/cn.qfei.makecli.daemon.plist`) so the daemon starts at login and is restarted on exit (`RunAtLoad` + `KeepAlive`). No `sudo` anywhere: the plist, logs and credentials all live under `$HOME` and the domain is `gui/$UID`. `start` runs the real preflight first (detect the local coding CLI, enroll with `--setup-key` when there is no node key yet) so failures surface in the terminal instead of looping inside launchd, then freezes the *resolved* configuration into the plist — launchd inherits no shell environment, so the gateway URL becomes an explicit argument and `PATH` / `HOME` / `MAKE_CLI_CONFIG_DIR` are written into `EnvironmentVariables`. `stop` unloads *and* disables the service but keeps the plist (a plist left loadable would silently start the daemon again at next login), `uninstall` unloads, clears that disable record and deletes the plist while leaving logs, the work dir and the node key untouched, `restart` reuses the on-disk plist so the arguments pinned at `start` are preserved, and `status` reports running / stopped / not installed plus a separate `Autostart` line — whether it is alive now and whether it comes back at login are two different questions (`--output table|json`). Logs land in `~/.make/agent/logs/daemon.log`; on non-macOS every lifecycle subcommand fails with a message pointing back at `makecli daemon --foreground` (#31, #32)
- **daemon**: The hidden `makecli daemon` subcommand joins the Make agent platform — it registers the machine, then drives a local coding CLI (claude-code / codex) through a heartbeat plus claim-polling loop, rendering each claim's identity and instructions into that CLI's native context file and parsing `@Name` mentions out of the final reply into structured mention blocks (#26, #28)

### Bug Fixes

- **daemon**: `daemon uninstall` now also clears the node key of the current profile, so rejoining requires a fresh setup key instead of silently reusing the old runtime identity (#33)
- **config**: Profile names are validated against a conservative grammar (`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`) and every persisted INI value rejects embedded newlines and leading/trailing whitespace, closing an INI section-injection window (e.g. a profile named `evil]\n[other` or a token value smuggling a `[section]` line)
- **config**: Atomic file replacement is platform-aware — on Windows the single-step overwrite rename (`MoveFileEx` + `REPLACE_EXISTING`) is retried with backoff when the destination is held open by another process, never touching the existing destination on failure; POSIX keeps plain atomic rename semantics

### Documentation

- **geb**: Convert bare-line blocks to markdown lists so the docs render correctly

## [v0.5.5] - 2026-07-14

### Features

- **skills**: Add the `makecli skills` command group — `skills list` merges the local lockfile with GitHub to flag each skill as installed / outdated / available (`--output table|json`), `skills update` installs missing and upgrades existing skills, and `skills remove <name>...` removes them after a source check that blocks deleting third-party skills (#21)
- **preflight**: Replace the `--app-type` skeleton check with a deterministic checklist derived from the build-service `build_spec.md` §5 — it auto-detects build mode A/B and the package manager (pnpm > yarn > npm), reports each item as ERROR/WARN/INFO with a per-failure "How to fix" block, and gates deploy/CI on the result (#21)

### Bug Fixes

- **schema**: Preserve the server-derived field `capabilities` (`groupable` / `sortable` / `supportsUniqueConstraint`) in `schema` output. They were silently dropped on the decode→re-encode round-trip, so the CLI diverged from the raw API response

## [v0.5.4] - 2026-07-06

### Features

- **whoami**: Add `makecli whoami` to show the user identity for the current access token (id, name, tenant) via the gateway `GET /user/v1/info`. Interaction mirrors `wrangler whoami`: when you are not logged in — or the token has expired — it auto-starts the login flow (at most one login per invocation), then renders a bordered `FIELD`/`VALUE` table; `--output json` is supported for scripting

## [v0.5.3] - 2026-07-02

### Bug Fixes

- **config**: Point the `production` environment preset to `qfei.cn` (Meta/Repo/Auth host triplet). The `dev` and `test` presets stay on `qtech.cn`; only production moved. A default-configured CLI now talks to `make.qfei.cn` out of the box

## [v0.5.2] - 2026-07-01

### Features

- **apply, diff**: Add `--max-depth` to control directory recursion (default `2`): `1` scans only the given directory (the previous behavior), `2` also descends into immediate subdirectories, `0` recurses the entire tree. Directory loading moved from single-level `os.ReadDir` to `filepath.WalkDir`; hidden files and hidden subdirectories (`.git` etc.) are never descended. `diff` shares the same flag so `diff` and `apply` agree on which files constitute an app

## [v0.5.1] - 2026-07-01

### Features

- **configure**: Add `configure resolve --target local-preview` — a token-free, offline command that resolves the active profile / environment / overrides into the minimal JSON (`make_api_origin`, `tenant_id`, `operator_id`) the Make platform skills need to wire up a local preview backend, instead of tooling having to re-derive the origin from scattered config (#20)
- **scaffold**: Sharpen the generated `AGENTS.md` guidance around `make app` local preview — clearer formal-login steps, local-preview auth handling, and environment resolution so a freshly scaffolded app points its preview at the right backend (#20)

### Features

- **config**: The backend environment now defaults to `production` (previously had no default), so a freshly configured CLI talks to the production platform out of the box. Override per-call with `--env dev|test|production` or persist it via `[settings] environment`
- **entity**: Support Entity-level unique constraints (`properties.uniqueConstraints`), matching the AgenticDSL update. The `entity create --json` file now carries the whole entity `properties` (`fields` + `uniqueConstraints`) — the same shape as the DSL YAML and `entity list -o json`'s `data.properties`, so the three round-trip. `apply` round-trips constraints from YAML; `diff` detects drift by constraint name with order-sensitive field comparison; `entity list <key>` renders a unique-constraints table in the detail view. The whitelist of constraint-eligible field types, the per-entity quota, and existing-duplicate rejection are enforced server-side
- **record**: Writes (`record create` / `record update`) that violate a unique constraint now surface a friendly `UniqueConstraintError` naming the conflicting constraint and fields, instead of the raw backend message
- **errors**: Authentication failures are now upgraded to an actionable message that guides you to run `makecli login`, echoing the active profile and environment

## [v0.4.6] - 2026-06-18

### Features

- **dry-run**: Add `--dry-run` to the create commands (`app` / `entity` / `relation` / `record create`), injecting an `X-Dry-Run` header so the backend runs the real validation but rolls back instead of persisting — answers "would this create succeed?" with no side effects. `app create --dry-run` short-circuits before any local scaffold, `git init`, or repo prep; the header is only sent on write operations, never on reads

### Refactor

- **preflight**: Rename the `--type` flag to `--app-type`

## [v0.4.5] - 2026-06-17

### Features

- **deploy**: `--env` now defaults to `preview` (no longer required); `production` must be opted into explicitly and prompts for a continue/abort confirmation before pushing — `--yes`/`-y` skips it, and non-interactive shells are refused with guidance to pass `--yes`. The internal repository clone URL is no longer printed
- **deploy**: Verify the app is registered on the Make platform before pushing. A project that was only `app init`'d (never `app create`'d) is now refused with guidance to run `makecli app create -f apps/dsl/app.yaml`, instead of silently pushing code for an unregistered app and leaving a repo-without-app orphan state
- **configure**: Add `--sample` to print a commented INI reference template, plus richer command examples so config keys are discoverable

## [v0.4.4] - 2026-06-17

### ⚠ Breaking Changes

- **deploy**: No longer auto-commits a worktree snapshot before pushing. `app deploy` now pushes the committed HEAD as-is and errors if the working tree is dirty, has no commits, or has no git repository — guiding you to commit first. Commit timing is handed back to the user

### Features

- **app**: Re-introduce `app init [appKey]` — scaffolds a complete local Make app project (`CLAUDE.md` / `AGENTS.md` / `apps/dsl/app.yaml` + `git init` + `.gitignore`), idempotent and remote-free. `app create` now shares the same scaffold core (`create` = `init` + remote registration + initial commit) and composes with a pre-existing `init` scaffold without clobbering edits
- **api**: Inject W3C `traceparent` + `X-Log-Id` headers on outbound requests for distributed tracing
- **login**: Add lark-cli-style progress messages to the browser OAuth flow

### Bug Fixes

- **deploy**: `gitSignature` reads the merged git config (LocalScope) so commits use the real user identity from `~/.gitconfig`, instead of only `/etc/gitconfig` (which almost always fell back to the `makecli` identity)

## [v0.4.3] - 2026-06-16

### Features

- **preflight**: Add `--type` flag (`fullstack`/`service`/`ui`, default `fullstack`) so headless and ui-only apps can gate CI/deploy without failing on absent siblings; `apps/dsl` stays required across all types

## [v0.4.2] - 2026-06-16

### Features

- **app**: Confirm before `app delete` by typing the app key (gh `repo delete` style), with `--yes`/`-y` to skip; non-interactive shells are refused with guidance to pass `--yes`. Requires Go 1.25.8+ to build (huh v2)

## [v0.4.1] - 2026-06-16

### ⚠ Breaking Changes

- **deploy**: Pure-Go git via go-git — no longer shells out to the system `git`; the binary is now self-contained. Reads the app key from `apps/dsl/app.yaml` (single source of truth, no `--app` flag) and auto-commits a worktree snapshot before pushing

### Features

- **app**: Add a DESCRIPTION column to the `app list` table (sourced from app properties)
- **update**: Sync Make platform skills during update (#17)

## [v0.4.0] - 2026-06-16

### ⚠ Breaking Changes

- **app**: Merge `app init` into `app create` — one command scaffolds the project, creates the remote App, and prepares code repositories; the standalone `app init` command is removed (#15)

### Features

- **update**: Add `--check` to report whether an update is available without installing (#16)
- **config**: Backend dev/test/production environment presets + host-only `--meta-server-url` (#14)

### Documentation

- Add `CHANGELOG.md`

## [v0.3.4] - 2026-06-15

### Features

- **login**: Browser OAuth login command — PKCE, single-hop discovery, RFC 7591 dynamic registration, ephemeral-port loopback callback
- **login**: Derive the OAuth server from the per-profile `auth-server-url`
- **deploy**: Add `app deploy` backed by the code repository service
- **preflight**: Add `preflight` to validate the app project layout
- **record**: Add `--filter` (raw CEL) to `record list`

### Bug Fixes

- **login**: Request `make:resources` scope instead of `mcp:*` scopes
- **login**: Ignore stray empty callback requests to prevent state mismatch

### Refactor

- **oauth**: Non-blocking callback send, single-switch `OpenBrowser`, test hardening

## [v0.3.3] - 2026-06-11

### Bug Fixes

- **filter**: Send the list filter as a CEL `Expression` object

## [v0.3.2] - 2026-06-02

### Features

- Add the runtime build contract to the app init template

### CI

- Skip Claude review/security jobs on fork PRs (#13)

## [v0.3.1] - 2026-05-30

### Features

- **update**: Verify the release checksum and harden the atomic install
- **notifier**: Background update-check notifier with an atomic cache and tri-state toggle
- **config**: `[settings]` global section with `LoadSettings`
- **build**: Derive `Date` from `vcs.time` when not injected via ldflags

### Bug Fixes

- **update**: Support Windows zip assets and bound stalled downloads
- **apply**: Distinguish not-found from transport errors via `api.ErrNotFound`
- **diff**: Exit non-zero on drift in JSON mode without swallowing errors
- **config**: Write config and credentials atomically; preserve `[settings]` across round-trip
- **notifier**: Suppress the notice on prerelease/dev builds; back off on refresh failure

### Refactor

- **api**: Collapse Entity/Relation writes into `writeResource` + `metaVersion`
- **cmd**: Use `slices.Contains` for the manifest extension check; add a `defaultName` helper

## [v0.3.0] - 2026-05-18

### ⚠ Breaking Changes

- Adopt the key/name DSL schema split (AgenticDSL MR #38)

## Earlier

Releases before v0.3.0 (v0.1.x–v0.2.x) predate this changelog. See the
[GitHub releases](https://github.com/qfeius/makecli/releases) for their notes.

[Unreleased]: https://github.com/qfeius/makecli/compare/v0.5.14...HEAD
[v0.5.14]: https://github.com/qfeius/makecli/releases/tag/v0.5.14
[v0.5.13]: https://github.com/qfeius/makecli/releases/tag/v0.5.13
[v0.5.12]: https://github.com/qfeius/makecli/releases/tag/v0.5.12
[v0.5.11]: https://github.com/qfeius/makecli/releases/tag/v0.5.11
[v0.5.10]: https://github.com/qfeius/makecli/releases/tag/v0.5.10
[v0.5.9]: https://github.com/qfeius/makecli/releases/tag/v0.5.9
[v0.5.8]: https://github.com/qfeius/makecli/releases/tag/v0.5.8
[v0.5.7]: https://github.com/qfeius/makecli/releases/tag/v0.5.7
[v0.5.5]: https://github.com/qfeius/makecli/releases/tag/v0.5.5
[v0.5.4]: https://github.com/qfeius/makecli/releases/tag/v0.5.4
[v0.5.3]: https://github.com/qfeius/makecli/releases/tag/v0.5.3
[v0.5.2]: https://github.com/qfeius/makecli/releases/tag/v0.5.2
[v0.5.1]: https://github.com/qfeius/makecli/releases/tag/v0.5.1
[v0.5.0]: https://github.com/qfeius/makecli/releases/tag/v0.5.0
[v0.4.6]: https://github.com/qfeius/makecli/releases/tag/v0.4.6
[v0.4.5]: https://github.com/qfeius/makecli/releases/tag/v0.4.5
[v0.4.4]: https://github.com/qfeius/makecli/releases/tag/v0.4.4
[v0.4.3]: https://github.com/qfeius/makecli/releases/tag/v0.4.3
[v0.4.2]: https://github.com/qfeius/makecli/releases/tag/v0.4.2
[v0.4.1]: https://github.com/qfeius/makecli/releases/tag/v0.4.1
[v0.4.0]: https://github.com/qfeius/makecli/releases/tag/v0.4.0
[v0.3.4]: https://github.com/qfeius/makecli/releases/tag/v0.3.4
[v0.3.3]: https://github.com/qfeius/makecli/releases/tag/v0.3.3
[v0.3.2]: https://github.com/qfeius/makecli/releases/tag/v0.3.2
[v0.3.1]: https://github.com/qfeius/makecli/releases/tag/v0.3.1
[v0.3.0]: https://github.com/qfeius/makecli/releases/tag/v0.3.0
