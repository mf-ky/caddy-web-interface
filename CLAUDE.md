# CLAUDE.md

All project guidance for AI agents lives in [AGENTS.md](AGENTS.md) — read it
first. Key rules in one breath: keep untouched Caddyfile blocks byte-identical,
never write Caddy JSON, every agent argument is validated, roles are enforced
in the Go API, no inline scripts (strict CSP), use `fill()` not
`replaceChildren()` in the UI, and run `make test` before committing.
