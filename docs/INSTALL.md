# Installing CaddyWeb — step by step

This guide assumes no programming knowledge. Copy each command exactly,
paste it into a terminal and press Enter. Lines starting with `#` are
explanations; you don't need to type them.

**How it fits together**

```
  Your browser  ──►  CaddyWeb (web app, port 8090)  ──SSH──►  caddyweb-agent  ──►  /etc/caddy/Caddyfile
                     any machine on your LAN                   on each Caddy server     + caddy reload
```

* **CaddyWeb** is the website you use. Install it once, on any Linux machine
  on your network — it can be the same machine that runs Caddy.
* The **agent** is a small script installed on each machine that runs Caddy.
  CaddyWeb connects to it over SSH with a key that can do *nothing* except
  read/validate/write the Caddyfile, manage its backups and reload Caddy.

---

## What you need

* A Linux machine for CaddyWeb (Debian, Ubuntu, DietPi, Raspberry Pi OS…;
  PC or any Raspberry Pi: amd64, arm64, armv7, armv6), **or** any machine with Docker.
* Your Caddy server(s), installed as a normal service
  (`/etc/caddy/Caddyfile`, `systemctl status caddy` works).
* SSH access to the Caddy server (you already use this to edit the Caddyfile).

---

## Step 1 — Install CaddyWeb

Pick **one** of the options.

### Option A: Linux service (recommended)

> **Note:** this downloads a published release from GitHub. If the command
> says the download failed, no release has been published yet — use Option B
> or C until one exists.

Log in to the machine that will run CaddyWeb and run:

```bash
curl -fsSL https://raw.githubusercontent.com/mf-ky/caddy-web-interface/main/scripts/install.sh | sudo bash
```

It downloads CaddyWeb, creates a `caddyweb` system user, stores its data in
`/var/lib/caddyweb`, and starts the `caddyweb` service on port **8090**. When
it finishes it prints the address to open, e.g. `http://192.168.0.5:8090`.

> Want another port? `curl -fsSL …/install.sh | sudo CADDYWEB_PORT=9000 bash`

### Option B: Docker

```bash
git clone https://github.com/mf-ky/caddy-web-interface.git
cd caddy-web-interface
docker compose up -d --build
```

CaddyWeb is now on port 8090 of that machine. Its data lives in a Docker
volume (named `caddy-web-interface_caddyweb-data`).

> With Docker, “this machine” inside the container is the container itself.
> When you add a Caddy server that runs on the same computer as Docker, use
> that computer's **LAN IP address** (e.g. `192.168.0.10`), not `127.0.0.1`.

### Option C: Build it yourself

With Go 1.24.7 or newer (plus git and make) installed: `make build` produces a single `caddyweb` binary.
Then `sudo CADDYWEB_BINARY=./caddyweb bash scripts/install.sh` installs it as
a service.

---

## Step 2 — Create your admin account

Open `http://<machine-ip>:8090` in your browser. The first time, CaddyWeb
asks you to create the **admin** account. Choose a username and a password
(at least 8 characters).

---

## Step 3 — Connect your Caddy server

1. On the **Servers** page press **Add a server**.
2. Give it a name (e.g. `DietPi`), type its **IP address** (e.g.
   `192.168.0.10`) and press **Save and continue**.
   Leave *SSH port* at 22 and *Agent user* at `caddyweb` unless you know you
   need something else.
3. The dialog now shows a command like this:

   ```bash
   curl -fsSL http://192.168.0.5:8090/agent/install.sh | sudo bash
   ```

   Open a terminal **on the Caddy server** (e.g. `ssh you@192.168.0.10`) and
   run that command. It prints what it does and ends with **“All done!”** and
   one or more *fingerprints* such as
   `SHA256:UZZ5++E340eKRG0rpb7EPxrB2Ebl6S4Ph2OHbeKx2bg  (ssh-ed25519)`.

   *Want to read the script before running it?* Open
   `http://<caddyweb-ip>:8090/agent/install.sh` in your browser first.

4. Back in CaddyWeb press **Test connection**. The first time you'll be asked
   to confirm the server's fingerprint — check that it matches one of the lines
   the installer printed, then press **It matches — trust this server**.
5. Press **Done**. Click the server's card: all your sites appear as cards.

Repeat step 3 for every Caddy server you want to manage. The same installer
command works for all of them.

### CaddyWeb on the same machine as Caddy?

That works too. Install CaddyWeb first (Option A), then follow step 3
normally and use `127.0.0.1` as the IP address. The agent installer reuses the
`caddyweb` user that CaddyWeb created. (With Docker, use the machine's LAN IP
instead — see the note under Option B.)

---

## Step 4 — Close Caddy's admin port (recommended)

If your Caddyfile contains `admin 0.0.0.0:2019`, anyone on your network can
reconfigure Caddy without a password. CaddyWeb doesn't need that. On the
server's page, the **Global settings** card shows a warning with a
**Fix it now** button: it changes the line to `admin localhost:2019`. Press
**Apply** to make it live.

(Don't use `admin off` — that stops `systemctl reload caddy` from working.)

---

## Step 5 — Try it

1. Press **New → Reverse Proxy**.
2. Name: `Test`, Web address: `test.yourdomain.com`,
   Send traffic to: `192.168.0.20:8080`.
3. Press **Add to draft**. A bar appears at the bottom: *1 change not live yet*.
4. Press **Review** to see exactly which lines will be added to the Caddyfile,
   then **Apply now**.

Caddy checks the new file first. If there's a mistake you'll see Caddy's
message and the exact line — and nothing on the server changes. If it's fine,
the old Caddyfile is backed up and Caddy reloads.

Open the Caddyfile on the server (`sudo nano /etc/caddy/Caddyfile`) and you'll
see your new block, written just like you'd write it by hand.

---

## Optional extras

* **More users** — *Users* page. Roles: **Admin** (everything), **Power User**
  (sees all cards and can add new site cards; can't change or delete live
  cards, apply, restore, or see passwords/API keys), **User** (read only,
  passwords/API keys hidden). On the command line these roles are called
  `admin`, `power` and `viewer`.
* **Backups** — every Apply/Restore saves the previous Caddyfile in
  `/etc/caddy/backups/` on the Caddy server (and a copy inside CaddyWeb). Set
  how many to keep in *Settings*. You can also have CaddyWeb copy them to a NAS
  with rsync after every change (*Settings → Extra copy with rsync*; authorize
  CaddyWeb's SSH key, shown on the same page, on the NAS).
* **DNS provider for certificates** — *Global settings* card → *Use a DNS
  provider*. CaddyWeb shows which providers are installed in your Caddy and the
  exact command to add a missing one.
* **HTTPS for CaddyWeb itself** — run `sudo systemctl edit caddyweb`, add
  ```
  [Service]
  Environment=CADDYWEB_TLS_CERT=/path/cert.pem CADDYWEB_TLS_KEY=/path/key.pem
  ```
  and `sudo systemctl restart caddyweb` (this survives upgrades), or put
  CaddyWeb behind Caddy. Keep port 8090 reachable on your LAN as well,
  so a broken Caddyfile can never lock you out of the tool that fixes it.

---

## Forgot your password?

On the machine that runs CaddyWeb:

```bash
sudo caddyweb user list                 # who exists
sudo caddyweb user passwd alex       # set a new password (it asks you)
sudo caddyweb user add alice --role admin
```

Docker: `docker exec -it caddyweb caddyweb user passwd alex`

---

## Updating

* **Option A**: run the install command from step 1 again. Your data and port
  are kept (other customisations belong in `sudo systemctl edit caddyweb`).
* **Option B**: `cd caddy-web-interface && git pull && docker compose up -d --build`.

---

## Uninstalling

Caddy never depends on CaddyWeb — your Caddyfile is a normal Caddyfile.

Do it in this order:

1. Remove the agent from each Caddy server (keeps the Caddyfile and backups,
   and puts the Caddyfile's original permissions back):
   ```bash
   curl -fsSL http://<caddyweb-ip>:8090/agent/install.sh | sudo bash -s -- --uninstall
   ```
2. Remove CaddyWeb (Option A):
   ```bash
   curl -fsSL https://raw.githubusercontent.com/mf-ky/caddy-web-interface/main/scripts/install.sh | sudo bash -s -- --uninstall
   ```
   Add `--purge` after `--uninstall` to also delete its data and the
   `caddyweb` user. Docker: `docker compose down` (add `-v` to delete the data
   volume too).

---

## Troubleshooting

| You see | What to do |
|---|---|
| *refused CaddyWeb's key* | The agent installer hasn't been run on that server (or was run from a different CaddyWeb). Run it again. If your SSH config uses `AllowUsers`, add `caddyweb` to it and restart ssh. |
| *cannot reach … connection refused / timeout* | Wrong IP or port, SSH not running on the server, or a firewall in between. `ssh you@server` from the CaddyWeb machine should work. |
| *the server's SSH key changed* | The server was reinstalled — or something is pretending to be it. If you reinstalled it, remove the server in CaddyWeb and add it again. |
| *agent can't write the Caddyfile* | Run the agent installer again; it fixes the permissions. |
| Apply fails mentioning the *admin endpoint* | Caddy isn't running (`sudo systemctl status caddy`) or the Caddyfile has `admin off`. |
| A DNS provider shows *not installed* | Run the `caddy add-package …` command CaddyWeb shows, then `sudo systemctl restart caddy`. |
| Anything else | Logs: `sudo journalctl -u caddyweb -f` (Docker: `docker logs caddyweb`) and `sudo journalctl -u caddy -f`. |

DietPi note: DietPi's default SSH server (Dropbear) is supported — the agent
key is locked with options both Dropbear and OpenSSH understand.
