# Self-hosting Lightspeak

Lightspeak has no central service: every group runs its own server, and the app connects to the server named in the invite. This guide covers three ways – **VPS** (recommended), **home server** and **local, for trying it out**.

## What you need

| | Minimum | Recommended |
|---|---|---|
| System | Debian 12+ or Ubuntu 22.04+ with systemd, x86_64 or arm64 | current Ubuntu LTS |
| CPU / RAM | 1 vCPU, 1 GB | 2 vCPU, 2 GB (server ≤ 384 MB, LiveKit ≤ 512 MB) |
| Disk | 2 GB free | 5 GB+ (attachments, default quota 4 GB) |
| Network | public IPv4 address | static IPv4 |
| Names | two host names pointing at the server | own domain or sslip.io |

These must be reachable: **80/TCP, 443/TCP** (HTTPS, certificates), **443/UDP** (HTTP/3, optional), **7881/TCP** and **7882/UDP** (voice and video). Lightspeak brings everything it needs: `install.sh` sets up the Lightspeak server, [LiveKit](https://livekit.io) (media server) and [Caddy](https://caddyserver.com) (HTTPS with Let's Encrypt certificates) as systemd services. No Docker required.

## Host names

Lightspeak needs two names for the same IP address:

- **App host**, e.g. `chat.example.org` – sign-in, chat, files, invite links, the browser client
- **RTC host**, e.g. `rtc.chat.example.org` – signalling for voice/video

**Own domain:** two A records (plus AAAA if you like) pointing at the server IP.

**No domain – [sslip.io](https://sslip.io):** names that contain the IP resolve automatically. For the IP `203.0.113.7`:
`lightspeak.203-0-113-7.sslip.io` and `rtc.203-0-113-7.sslip.io`. Downside: Let's Encrypt limits certificates per domain and sslip.io is popular, so issuing can fail at times. Then switch to your own domain later (run `install.sh` again, send new invite links).

## Option 1: VPS (recommended)

Any small VPS will do (2 vCPU and 2–4 GB of RAM are plenty). Steps:

1. **Get the installer** (on the server):
   ```
   curl -fsSLO https://github.com/syntstar/lightspeak/releases/latest/download/install.sh
   ```
2. **Install:**
   ```
   sudo bash install.sh --version latest
   ```
   The script asks for the app host, RTC host, the server name shown to users and who may create accounts (see [Registration](#registration-ls_signup)), shows a summary and only changes something after you confirm. Skip the question about a separate account for remote updates with Enter unless you want one (see [Updates](#updates)). If another web server already uses 80/443, it stops before doing anything. The server and the browser client are downloaded and only installed if their checksums are signed with the release key.
3. **Provider firewall:** if your provider has its own cloud firewall or security groups, open 80/TCP, 443/TCP, 443/UDP, 7881/TCP and 7882/UDP there. A local `ufw` is configured by the script (and only enabled after asking; SSH stays allowed).
4. **Owner invite:** at the end you get a link `https://<app-host>/invite/<code>` (valid 24 h, single use). Open it in a browser, download the app from there (or use the browser client) and create your account – it becomes the server admin and owner of the first space.

Unattended installs use an answers file (`--config answers.env --yes`); all keys are listed at the top of `install.sh` (`sudo bash install.sh --help`), and server settings like `LS_MAX_SPACES=5` work there too. Unknown keys stop the installation, so a typo cannot hide.

**Build it yourself instead of downloading** (Go version from `server/go.mod`, on your PC, not on the server; on Windows `./scripts/release.ps1` does this):
```
cd server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=1.2.3" -o lightspeak-server-linux-amd64 ./cmd/lightspeak-server
```
Copy the binary (and optionally the browser client `lightspeak-web.tar.gz`) to the server together with `deploy/install.sh`, then:
```
sudo bash install.sh --server-binary ./lightspeak-server-linux-amd64 --web-archive ./lightspeak-web.tar.gz
```

**What runs afterwards:** `lightspeak-server` (127.0.0.1:8080 only, behind Caddy), `lightspeak-livekit` (signalling local only, media on 7881/7882), `caddy` (80/443), `lightspeak-backup.timer` (daily 04:30). Programs live in `/opt/lightspeak`, settings and secrets in `/etc/lightspeak`, data in `/var/lib/lightspeak`.

## Option 2: Home server (Raspberry Pi, old PC, NAS running Debian)

Same as the VPS route, plus:

- **A public IPv4 address is required.** Many connections (fibre, cable, mobile) only have **DS-Lite/CGNAT** – then the server cannot be reached over IPv4 from outside and port forwarding has no effect. Check: if the router's WAN address is in `100.64.0.0/10` or it only has IPv6, you are behind CGNAT. Fix: ask your ISP for a public IPv4 (often free on request) or use a small VPS.
- **Port forwarding on the router** to the server's local IP: 80/TCP, 443/TCP, 443/UDP, 7881/TCP, 7882/UDP. Give the server a fixed local IP in the router.
- **Changing IP:** set up a dynamic DNS name (e.g. from your router vendor or [deSEC](https://desec.io)) for the app and RTC hosts; sslip.io does not work here because the IP is part of the name. LiveKit detects the public IP at start-up (`use_external_ip`); after an IP change run `sudo systemctl restart lightspeak-livekit`.
- **Clients on the same network:** Behind NAT the installer makes LiveKit announce its local address too. Devices on your home network then connect directly over UDP, even if the router does not loop UDP back from its own public IP (hairpin NAT). Otherwise video runs over TCP and stays blurry.
- **Upload bandwidth:** screen sharing in 1080p needs 5–8 Mbit/s of server upload per viewer. With a slow upload, pick a lower stream quality in the app.
- arm64 (Raspberry Pi 4/5 with a 64-bit OS) is supported: use `lightspeak-server-linux-arm64`.

## Option 3: Try it locally (no internet, no HTTPS)

For development or a quick test on one PC. The app only allows `http://` for `localhost`/`127.0.0.1`.

1. Start LiveKit in dev mode: download [livekit-server](https://github.com/livekit/livekit/releases), run `livekit-server --dev --bind 127.0.0.1`.
2. Server: `cd server && go run ./cmd/lightspeak-server serve --dev` (port 8080, data in `server/dev-data`).
3. Owner invite: `go run ./cmd/lightspeak-server admin --dev bootstrap`.
4. In the app, enter `127.0.0.1:8080` as the server, then use the invite code.

Others on the same network **cannot** connect this way (no HTTPS). Use one of the other two options for that.

## Registration (`LS_SIGNUP`)

| Value | Who can create new accounts? |
|---|---|
| `admin` (default) | only with invites from server admins (app or `lightspeak-admin create-invite`) |
| `invite` | with any invite, including those of space admins |
| `closed` | nobody (only the owner invite from `bootstrap`) |

Invites to a space always work for people who already have an account on the server. Guest access (an invite that lets people in with just a name, see the [admin guide](ADMIN_GUIDE.md#in-the-app)) counts as creating an account: it works for exactly the invites that could also create one. To change it: edit `LS_SIGNUP` in `/etc/lightspeak/server.env` and run `sudo systemctl restart lightspeak-server`.

## Invites (`LS_INVITES`)

| Value | Who can invite people to a space? |
|---|---|
| `members` (default) | everyone with the space permission "Invite people" (owner, roles) |
| `admins` | only server admins and `lightspeak-admin create-invite`; the app hides the menu entry for everyone else |

Switching to `admins` also invalidates links other people created earlier. They work again if you switch back.

## Settings

All settings live in **`/etc/lightspeak/server.env`**. Your own lines go **below** `# --- own settings ---` and survive updates and re-running `install.sh`. The template with every value and an explanation is [`deploy/server.env.example`](../deploy/server.env.example). Afterwards:

```
sudo lightspeak-admin config                # checks the values, shows them (no secrets), reports typos
sudo systemctl restart lightspeak-server
```

| Variable | Meaning | Default | Allowed |
|---|---|---|---|
| `LS_SIGNUP` | who may create accounts | `admin` | `admin`, `invite`, `closed` |
| `LS_INVITES` | who may invite to spaces | `members` | `members`, `admins` |
| `LS_MAX_SPACES` | spaces per regular account (0 = only server admins create them; server admins unlimited) | 3 | 0–100 |
| `LS_MAX_MESSAGE_CHARS` | characters per message | 4000 | 100–20000 |
| `LS_MAX_SPACE_MEMBERS` | members per space | 500 | 2–1000 |
| `LS_MAX_SPACE_CHANNELS` | channels per space, categories included | 200 | 10–1000 |
| `LS_MAX_SPACE_ROLES` | roles per space | 25 | 2–100 |
| `LS_RETENTION_DAYS` | delete attachments after N days (0 = keep) | 0 | |
| `LS_STORAGE_QUOTA_GB` | storage for attachments; when full, the server refuses uploads | 4 | |
| `LS_DOWNLOAD_URL` | download link for the Windows app on the invite page | the latest GitHub release | |
| `LS_MIN_CLIENT_VERSION` | older apps are asked to update | 0.1.0 | |

The app picks these values up when it connects, so its checks and error messages match the server.

**Two values need `reconfigure`,** because LiveKit and Caddy have to know them as well. They sit above the marker in `server.env`; change them there and run `sudo lightspeak-admin reconfigure` (restarting LiveKit drops running calls):

| Variable | Meaning | Default | Allowed |
|---|---|---|---|
| `LS_MAX_VOICE_PARTICIPANTS` | people per voice channel, moderators included | 25 | 2–100 |
| `LS_MAX_UPLOAD_MB` | largest file in MB | 200 | 1–51200 |

**Deliberately not configurable** are protective limits such as rate limits, image sizes and WebSocket frames. A wrong value there would open the server to abuse.

`sudo lightspeak-admin storage-report` shows the largest files, `sudo lightspeak-admin purge-attachments --days 90` cleans up.

## Administration on the server

```
sudo lightspeak-admin update                                   # newest signed release
sudo lightspeak-admin config                                   # check and show the settings
sudo lightspeak-admin reconfigure                              # apply server.env to Caddy, LiveKit, services
sudo lightspeak-admin list-users
sudo lightspeak-admin create-invite --hours 168 --uses 1      # account invite (7 days, single use)
sudo lightspeak-admin reset-password <username>                # prints a temporary password
sudo lightspeak-admin disable-user <username>                  # or enable-user
sudo lightspeak-admin bootstrap --force                        # new owner invite
sudo journalctl -u lightspeak-server -n 200 --no-pager         # logs (also lightspeak-livekit, caddy)
```

More for admins (roles, invites in the app, storage, troubleshooting): [ADMIN_GUIDE.md](ADMIN_GUIDE.md).

## Updates

- **Usual case, on the server itself:** `sudo lightspeak-admin update` fetches the newest release, checks the signature, backs up the database, switches over, checks the server and rolls back automatically on failure. A specific version: `--version 1.2.3`. A downgrade asks first, because database changes are not undone.
- **If a release brings a new installer** (new LiveKit version, Caddy or systemd changes), `update` tells you. Then run `sudo lightspeak-admin reconfigure` when convenient, ideally while nobody is in a call.
- **The Windows app** updates itself from the GitHub releases of the project, independent of your server. Keep the server reasonably current so both fit together; `LS_MIN_CLIENT_VERSION` lets you ask very old apps to update.
- **Without a root login:** if you created the optional deploy account (`LS_DEPLOY_USER`, SSH key only), it may run exactly this without a password: `ssh lsdeploy@<app-host> sudo lightspeak-admin update`.
- **Self-built:** `sudo bash install.sh --server-binary … --web-archive …` does the same steps.
- **Your own fork or mirror:** put `LS_RELEASE_URL` and `LS_RELEASE_PUBKEY` (your own key, see [CONTRIBUTING.md](../CONTRIBUTING.md#releases)) into the answers file.

## Backups and restore

- The database is backed up daily to `/var/lib/lightspeak/backups/lightspeak-<date>.db` (3 generations) and before every update. Manually: `sudo lightspeak-admin backup`.
- **Attachments** (`/var/lib/lightspeak/files/`) are not part of the backup. To keep them, copy the folder elsewhere as well (e.g. `rsync` to another machine).
- **Keep off-server for emergencies:** the backup file, `/etc/lightspeak/` (contains keys – treat as confidential) and optionally `files/`.

Restore:
```
sudo systemctl stop lightspeak-server
sudo cp /var/lib/lightspeak/data/lightspeak.db /var/lib/lightspeak/data/lightspeak.db.before-restore
sudo rm -f /var/lib/lightspeak/data/lightspeak.db-wal /var/lib/lightspeak/data/lightspeak.db-shm
sudo install -o lightspeak -g lightspeak -m 0640 /var/lib/lightspeak/backups/lightspeak-<date>.db /var/lib/lightspeak/data/lightspeak.db
sudo systemctl start lightspeak-server
```
On a new server: run `install.sh` first (same host names), then restore the database as above and copy `files/` back (`chown -R lightspeak:lightspeak`). Accounts, spaces and messages are then as of the backup.

## Security in brief

- Only Caddy (80/443) and LiveKit media (7881/7882) are public; the server and the LiveKit API listen on 127.0.0.1 only.
- Services run as dedicated system users with systemd hardening; secrets live in `/etc/lightspeak/*.env` (0640) and are never printed.
- Passwords: Argon2id; sessions use tokens, stored by the app in the Windows Credential Manager.
- Calls and screen shares are end-to-end encrypted; chat and files are stored unencrypted on the server (as with Discord) – whoever runs the server can read them.
- Details: [SECURITY.md](SECURITY.md).

## Running a server for other people

- **You are responsible for their data.** Messages, files and account names are stored on your machine. If you host for more than your own household, tell people what is stored and who can access it, keep backups safe and honour deletion requests. Users can delete their own account in the app (**Settings → My account**); old attachments can be removed with `lightspeak-admin purge-attachments`. In the EU, the GDPR applies to you as the operator.
- **Bandwidth is the limit, not CPU.** A voice participant needs about 30–100 kbit/s in each direction. A screen share needs 2.5–15 Mbit/s of upload from the streamer, and the server sends that again to every viewer. Choose `LS_MAX_VOICE_PARTICIPANTS` with your server’s upload in mind.
- **Memory:** the three services together idle at roughly 250 MB. 1 GB of RAM is enough for a group of friends; 2 GB leaves comfortable headroom.
- **Source code:** Lightspeak is licensed under AGPL-3.0. If you run a modified server for others, you must offer them the source code of your version.

## Uninstall

```
sudo systemctl disable --now lightspeak-server lightspeak-livekit lightspeak-backup.timer
sudo rm -f /etc/systemd/system/lightspeak-* /etc/caddy/lightspeak.caddy /etc/sudoers.d/lightspeak /usr/local/bin/lightspeak-admin
sudo rm -rf /usr/local/lib/lightspeak
# remove the line "import /etc/caddy/lightspeak.caddy" from /etc/caddy/Caddyfile, then:
sudo systemctl daemon-reload && sudo systemctl reload caddy
sudo rm -rf /opt/lightspeak /usr/local/lib/lightspeak-livekit /etc/lightspeak /var/lib/lightspeak   # deletes all data!
```
Remove the system users (`lightspeak`, `livekit`, the deploy account if any) with `sudo userdel`; ufw rules with `sudo ufw status numbered` / `sudo ufw delete <no>`.
