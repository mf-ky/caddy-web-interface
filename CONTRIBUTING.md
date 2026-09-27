# Contributing to CaddyWeb

Thanks for helping! Bug reports, ideas and pull requests are all welcome.

## Reporting a bug

Open an issue and include:

- what you did, what you expected, and what happened instead
- your Caddy version (`caddy version`) and how CaddyWeb is installed (service or Docker)
- the error message shown in CaddyWeb, and anything relevant from
  `journalctl -u caddyweb` / `docker logs caddyweb`

**Remove secrets first** — API keys, password hashes, and anything else in your
Caddyfile you don't want public. For security problems, please don't open a
public issue; use GitHub's *Report a vulnerability* button (Security tab)
instead.

## Pull requests

1. Read [AGENTS.md](AGENTS.md) — it explains how CaddyWeb works and the rules
   that keep users' Caddyfiles safe (for example: untouched blocks must stay
   byte-for-byte identical, and every value sent to the server is validated).
2. Keep changes focused: one fix or feature per pull request.
3. Run the checks before opening it:
   ```bash
   make test            # needs Go 1.24.7+; install Caddy to also run the end-to-end test
   for f in web/static/js/*.js web/static/js/views/*.js; do node --check "$f"; done
   bash -n agent/caddyweb-agent agent/install-agent.sh scripts/install.sh
   ```
4. If you change what users see or do, update the docs too: `docs/INSTALL.md`,
   the in-app Help page (`web/static/js/views/help.js`) and `README.md`.
5. Never include real domains, IPs or keys in tests, docs or screenshots — use
   `example.com` and made-up addresses.

AI-assisted contributions are fine; please review and test them like any other
change.

By contributing you agree that your contribution is released under the
project's [MIT License](LICENSE).
