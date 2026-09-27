# AGENTS.md — guide for AI agents (Claude, Codex, Copilot, …) and new contributors

Read this before changing anything. It explains how CaddyWeb works, the rules
that must not be broken, and how to make common changes safely.

## What CaddyWeb is

A self-hosted web UI that manages one or more [Caddy](https://caddyserver.com)
servers by **editing their real Caddyfile** (never Caddy's JSON config). Sites
are shown as cards; edits go into a per-server *draft*; **Apply** validates,
backs up, writes and reloads. Users with roles (admin / power / viewer) sign in;
passwords can be reset from the CLI.

Owner's priorities (keep them): the Caddyfile must stay hand-editable and keep
comments/layout; uninstalling CaddyWeb must leave Caddy fully working; clear
error messages; beginner-friendly UI and docs; security on a home LAN.

## Architecture

```
browser ──HTTP──► caddyweb serve (Go, one binary, embeds the UI)
                      │   data dir: users.json, settings.json, servers.json,
                      │             servers/<id>/{draft,live,history}.json + backups/
                      └──SSH (forced command) or exec (same machine)──► caddyweb-agent (bash)
                                                                         └─► /etc/caddy/Caddyfile, /etc/caddy/backups, caddy validate/reload
```

* **No Caddy admin API over the network.** The agent runs `caddy validate` and
  `caddy reload` locally on the Caddy host. This is deliberate (the API can't
  write the Caddyfile to disk, so changes would be lost on restart).
* **The agent is the only thing the SSH key can run.** `install-agent.sh`
  writes `command="/usr/local/bin/caddyweb-agent",no-port-forwarding,…` into
  `authorized_keys` (options valid for OpenSSH *and* Dropbear).

## Repository map

| Path | What |
|---|---|
| `cmd/caddyweb/main.go` | CLI: `serve`, `user list/add/passwd/role/delete`, `pubkey`, `installer`, `version`. |
| `internal/caddyfile/` | **Lossless** Caddyfile lexer/parser/formatter. `Parse` → `Document{Parts}`; each `Segment` (global/site/snippet/namedroute/directive) keeps its original bytes (`raw`) until edited (`MarkDirty`). `nodes.go`: `ResolveRaw`/`CheckSegment` validate UI input; `redact.go` masks secrets for non-admins. |
| `internal/remote/` | Talks to the agent: `remote.go` (SSH client with trust-on-first-use host keys, local exec), `agent.go` (typed commands, `Explain` turns agent/Caddy output into `CaddyError{kind,message,line}`). |
| `internal/store/` | JSON persistence (atomic writes, owner-preserving for `sudo caddyweb user …`). `users.go` (bcrypt, roles, `Stamp` invalidates sessions on password/role change), `state.go` (settings, servers, per-server `ServerState`, migration from single-server layout). |
| `internal/server/` | HTTP API. `server.go` routes + security middleware, `auth.go` sessions/login/setup, `servers.go` per-server context (`serverCtx`), overview, add/edit/remove server, copy card, hash tool, `draft.go` draft model & card mutations, `apply.go` validate/apply/backups/restore/rsync, `admin.go` settings & users. `server_test.go` = end-to-end test with real Caddy. |
| `internal/diff/` | Line diff for Review and backups. |
| `agent/caddyweb-agent` | Bash agent (commands: version, info, read, validate, apply, backups, backup-read, restore, prune, modules). Embedded into the Go binary. |
| `agent/install-agent.sh` | Installer template; `__PUBLIC_KEY__`/`__AGENT_SCRIPT__` are filled by `agent.Installer()` and served at `GET /agent/install.sh`. Supports `--uninstall`. |
| `web/static/` | The UI: plain ES modules, no build step. `js/app.js` (shell, router, login), `js/lib.js` (DOM `h()`, `fill()`, icons, api, modal/drawer, toasts), `js/caddy.js` (quoting, classification, catalogs: templates, header presets, DNS providers), `js/directives.js` (per-directive editors), `js/editor.js` (card drawer, cert section, global editor), `js/views/*` (pages), `css/app.css` (tokens, light/dark). |
| `scripts/install.sh` | Installs the binary + systemd unit (downloads GitHub release). |
| `Dockerfile`, `docker-compose.yml`, `Makefile`, `.github/workflows/` | Packaging, CI, release on tag `v*`. |
| `docs/INSTALL.md` | Beginner install guide. The in-app Help page is `web/static/js/views/help.js` — keep both in sync. |

## Invariants — do not break these

1. **Never rewrite blocks the user didn't edit.** Untouched segments must be
   written back byte-for-byte (see `TestRoundTripUserFile`). Edited segments are
   re-formatted with the file's own indentation (`Document.Indent`) and line
   endings (`CRLF`).
2. **Never write Caddy JSON** or use the admin API for configuration.
3. **Validate before write; back up before write; roll back on reload failure.**
   This lives in the agent's `install_file`. Keep exit codes stable:
   0 ok, 1 error, 2 invalid config, 3 reload failed (rolled back), 4 conflict
   (file changed since read — optimistic lock by SHA-256), 5 no such backup.
4. **Agent input is untrusted.** Every argument passes `safeArg` in Go and a
   strict check in bash (backup names match a regex; no paths). Don't add
   commands that take free-form paths or execute input.
5. **Token safety.** Values from the UI become Caddyfile tokens only through
   `checkToken` (single token, no newlines/braces). Free text goes through
   `{raw}` nodes and is parsed by `ParseNodes` — never string-concatenated.
6. **Roles are enforced server-side** (`require`/`onServer` + `canEdit`):
   viewer = read only (secrets redacted); power = add site cards, edit only
   their own not-yet-applied cards, copy site cards, validate; admin = all.
   The UI hides buttons, but the API is the gatekeeper.
7. **CSRF**: every non-GET `/api/*` request needs header `X-CaddyWeb: 1`
   (and a same-host Origin). Cookies are HttpOnly + SameSite=Strict.
8. **CSP is strict** (`script-src 'self'`): no inline `<script>`, no `eval`,
   no CDN assets. Everything is served from `web/static`, embedded at build.
9. **Drafts are optimistic-locked** with `rev`: mutations send `?rev=N`; a
   mismatch returns 409 with fresh `state`. Keep this for every new mutation.
10. **Secrets never reach non-admins**: use `caddyfile.RedactSegment/RedactText`
    in any new endpoint that returns Caddyfile content.

## Running & testing

```bash
make test          # go vet + all tests; the e2e test runs only if `caddy` is on PATH
make run           # http://localhost:8090, data in ./data
node --check web/static/js/*.js web/static/js/views/*.js   # JS syntax
bash -n agent/caddyweb-agent agent/install-agent.sh scripts/install.sh
```

Try the agent without SSH: create a config file with `CADDYFILE=…`,
`BACKUP_DIR=…`, `STAGING_DIR=…` and run
`CADDYWEB_AGENT_CONF=/path/conf agent/caddyweb-agent info` (env override is
ignored when invoked over SSH). In the UI, add a server in **This same
machine** mode with *Agent path* pointing to a wrapper script that exports
`CADDYWEB_AGENT_CONF` (see `startCaddy` in `internal/server/server_test.go`).

For UI checks, Playwright/Chromium works well: log in, click through, and
watch for console errors.

## How to…

**Add a directive editor** — `web/static/js/directives.js`: write
`function fooEditor(node, ctx)` that edits `node.tokens` / `node.block` *in
place* (never rebuild nodes wholesale: comments and unknown sub-options must
survive), register it in `DIRECTIVES` with `label`, `icon`, `group`, `desc`,
`make()`. Use `otherOptions()` for sub-options you don't model. If it should
show on cards, extend `summarize()`/`classify()` in `caddy.js`.

**Add a card template (New menu)** — `templates()` in `caddy.js`.

**Add a DNS provider** — `DNS_PROVIDERS` in `caddy.js`: `id` (module name after
`dns.providers.`), `name`, `fields` `[key, label, secret?]`; set `single: true`
if the provider takes one argument (`acme_dns id TOKEN`) or `positional: true`
for several arguments. Check the provider's README at
`github.com/caddy-dns/<id>` for the Caddyfile syntax.

**Add an API endpoint** — register in `Handler()` (`server.go`): global routes
via `any/power/admin(...)`, per-server routes via `srv("METHOD /path", h, roles)`
where `h(w, r, u, sc)` gets the `serverCtx`. Draft changes must go through
`s.mutate(...)`. Return errors with `writeErr`/`writeErrData`; Caddy/agent
failures with `caddyErrorResponse` so the UI can point at the line/card.
Add a case to `server_test.go`.

**Add an agent command** — add a `case` in `agent/caddyweb-agent`, validate
every argument, add a typed wrapper in `internal/remote/agent.go`. Bump
`AGENT_VERSION` if CaddyWeb depends on the new command, and tell users to
re-run the installer (the UI shows the agent version under server info).

**Change the look** — tokens at the top of `css/app.css` (light in `:root`,
dark twice: `@media (prefers-color-scheme: dark)` for "system" and
`[data-theme="dark"]` for the explicit choice — keep both copies identical).

## UI conventions & gotchas

* Build DOM with `h(tag, props, ...children)`; replace children with
  `fill(el, ...)` — **not** `el.replaceChildren(...)`, which renders `null` as
  the text "null".
* Editors re-render only on structural changes (add/remove), never on every
  keystroke, so inputs keep focus.
* Every page must work at phone width (≈390px) and in both themes.
* Tokens in nodes are *raw* Caddyfile text: use `quote()`/`unquote()`.
* Keep wording friendly and concrete; explain consequences ("nothing on the
  server changes") — the owner is not a programmer.

## Data directory layout

```
/var/lib/caddyweb/          (Docker: /data)
  users.json  settings.json  servers.json  session.key
  ssh/id_ed25519(.pub)  ssh/known_hosts_offsite
  servers/<id>/draft.json  live.json  history.json  backups/Caddyfile.<timestamp>
  servers/.removed/<id>-<time>/     (forgotten servers)
```

## Release

Tag `vX.Y.Z` and push: `.github/workflows/release.yml` builds
`caddyweb-linux-{amd64,arm64,armv7,armv6}` + `SHA256SUMS`, which
`scripts/install.sh` downloads.

## Before you commit

- [ ] `make test` passes (install Caddy locally to run the e2e test).
- [ ] JS passes `node --check`; no console errors when clicking through.
- [ ] Round-trip test still byte-identical; new inputs validated server-side.
- [ ] Docs updated: `docs/INSTALL.md`, Help page, README, this file.
