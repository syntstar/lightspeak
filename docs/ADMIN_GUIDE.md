# Lightspeak admin guide

For the person who runs a server. Setting up a new server is covered in [SELF_HOSTING.md](SELF_HOSTING.md); your friends only need the [user guide](USER_GUIDE.md).

## Roles

| Level | May |
|---|---|
| **Server admin** (the account created with the owner invite) | Everything in every space they are a member of – nobody can remove, ban or mute them. **Settings → Server**: see all spaces on the server, join them, become their owner, delete them. Create invites that allow new accounts (with `LS_SIGNUP=admin`). Create any number of spaces. |
| **Every account** | Create own spaces (at most `LS_MAX_SPACES`, default 3), add friends by username, send direct messages and call friends. |
| **Owner** of a space (whoever created it) | All permissions in the space. Only the owner (and the server admin) can **delete** or **transfer** the space (“Make owner …” in a person’s menu). |
| **Roles** (per space) | Individually selectable permissions: manage space, manage channels, manage roles, invite people, remove members, ban, moderate voice channels, manage messages, mention @everyone, and *Administrator* (everything except owner rights). Every space starts with an **“Admin”** role (Administrator). **@everyone** applies to all members (default: no extra permissions). |
| **Member** without a role | Write, talk, share their screen, leave the space. |

**Server-wide rules:** `LS_INVITES=admins` restricts inviting to server admins, regardless of the “invite people” permission. Limits such as characters per message or members, channels and roles per space are set in `/etc/lightspeak/server.env`: see [SELF_HOSTING.md → Settings](SELF_HOSTING.md#settings).

**Hierarchy:** roles are ordered (Space settings → Roles, arrows). You can only manage people and roles *below* your own highest role, and only grant permissions you hold yourself. So an admin cannot remove another admin, and nobody can promote themselves.

**Can the server admin listen in?** Even the server admin can only read the messages and decrypt the calls of a space after joining it – and everyone sees that in the member list (marked “Server admin”). A change of owner is shown to everyone in the space. Keep in mind that text messages and files are stored unencrypted on the server, so whoever has shell access to the machine can read them; see [SECURITY.md](SECURITY.md).

Everything is checked on the server; the app merely hides what you are not allowed to do.

## In the app

- **Space menu** (click the space name at the top left, or right-click the space icon): **Invite people**, **Create channel**, **Space settings**.
- **Inviting:** choose how long the link is valid (1 hour … unlimited) and how often it may be used, then **Create and copy link**. All active invites are listed below with a usage counter – **×** revokes one immediately.
- **Guests:** with **Allow guest access** switched on, the link also lets people in with just a name. A guest is a temporary account without a password: member of this one space only, with the permissions of @everyone plus any roles you give them. Guests cannot create or join other spaces, create invites, add friends, change their username or become owner. They are marked **Guest** in the member list. A guest is removed for good when they sign out, when you remove or ban them, or after 30 days without activity; their messages stay as written by a deleted user. Use a low number of uses and a short validity for guest links: everyone who has the link can come in. Guest links follow the same rule as new accounts (`LS_SIGNUP`): with `admin` only links created by a server admin admit guests, with `closed` none do.
- **Links for a friend to pass on:** with `LS_SIGNUP=admin` only your invites (as server admin) create new accounts. If a friend wants to bring someone along, create a few **single-use** links for them. They do not need to become an admin. Whoever uses a link lands in your space and can then be invited to your friend’s spaces as well. Revoke unused links in the same list.
- **Creating channels and categories:** right-click a category or the empty area of the channel list → **Create text channel / voice channel / category**, or use the **+** next to a heading. Categories can be renamed and deleted like channels (their channels stay and move to the top, without a category).
- **Sorting:** **drag** channels and categories with the mouse – a line shows where they will land; dropping a channel on a category heading moves it inside. Without a mouse: right-click → **Move up / Move down** or **Move to category**. Collapsing a category only affects you.
- **Channels:** right-click a channel → **Edit channel**, **Move up / Move down**, **Move to category**, **Delete channel** (deletes all messages and files in it).
- **Edit channel** has three tabs:
  - **General:** name, topic, category and **icon**. Text channels also have **slow mode** (a pause between two messages per person; moderators are exempt). Voice channels also have a **capacity** and the **Away channel** switch: nobody can speak there, and under **Move inactive people here** you choose after how many minutes without speaking and without using the PC people are moved in automatically. There is one away channel per space.
  - **Audio** (voice channels): **Voice** (24 kbit/s, default), **High** (48 kbit/s) or **Music** (96 kbit/s, without cutting out in quiet passages). Applies to every microphone in the channel, immediately.
  - **Permissions** (needs “manage roles”): on the left **@everyone** and the roles that differ in this channel (**Add role**), on the right, per permission, **✕ Deny · – Default · ✓ Allow**. “Default” takes the role’s setting from the space settings. Voice channels: **Join**, **Speak**, **Share screen**. Text channels: **Send messages**, **Send files and images**, **React**, **Mention @everyone**. An allow on one role beats a deny on another.
  - Examples: **Announcements** = @everyone “Send messages” ✕, role “News” ✓. **Stage** = @everyone “Speak” and “Share screen” ✕, role “Stage” ✓. **Only for one role** = @everyone “Join” ✕, role ✓.
  - **Private channel:** set **View channel** to ✕ for @everyone and to ✓ for the roles that should see it. For everyone else the channel is completely invisible (messages, who is in the voice channel, search); whoever loses the role stops seeing it immediately. The away channel is always visible to everyone. Category names are visible to all members; a category without visible channels is hidden.
  - Administrators (and the owner and server admin) are never restricted – except that nobody speaks in the away channel. You can only change roles below your own and only permissions you hold yourself. Changes apply immediately, also to people who are already in the channel.
- **Space settings → Roles:** create, rename, sort and delete roles and set their permissions with switches (saved immediately). The channel permissions (join, speak, share screen, send messages, files, react) also exist space-wide and are on for **@everyone** by default. If you turn them off there, that applies in every channel that does not explicitly allow them.
- **Members:** click or right-click a person in the member list (or **Space settings → Members → …**) → tick or untick **roles**, **Make owner …** (owner and server admin only), **Remove from space** (they can come back with a new invite), **Ban** (no way back until you unban them under **Banned**).
- **Space settings → General:** name and picture (permission “manage space”), **Delete space** (owner and server admin only; type the name to confirm; cannot be undone).

## Accounts (on the server)

`lightspeak-admin` runs as root on the server:

```
sudo lightspeak-admin list-users
sudo lightspeak-admin create-invite --hours 168 --uses 1
sudo lightspeak-admin create-invite --space <space-id> --uses 5
sudo lightspeak-admin reset-password <username>
sudo lightspeak-admin disable-user <username>      # or enable-user
sudo lightspeak-admin bootstrap --force            # new owner invite
```

- `create-invite` without `--space` only creates an account; with `--space` (the ID is under right-click → *Copy space ID*) the person joins that space directly. `--hours 0` / `--uses 0` mean unlimited.
- `reset-password` prints a temporary password and signs the person out everywhere. Lightspeak has no e-mail addresses, so this is the only way to recover an account. Pass the password on through a safe channel and have the person change it right away under *Settings → My account*.
- `disable-user` locks an account server-wide (sign-in and running sessions) without deleting any data.
- `bootstrap --force` is only for the case that you have lost access to your admin account; older unused owner invites become invalid.

If you created the optional deploy account during installation, the same commands work from your own computer without a password: `ssh lsdeploy@<app-host> sudo lightspeak-admin list-users`.

## Updates

`sudo lightspeak-admin update` installs the newest signed server release, with a database backup and an automatic rollback: [SELF_HOSTING.md → Updates](SELF_HOSTING.md#updates). Changes to host names, ports or limits go into `/etc/lightspeak/server.env`, followed by `sudo lightspeak-admin reconfigure`.

The Windows app updates itself from the project’s GitHub releases; there is nothing to do on your server.

## Backups

- Automatic, every day at 04:30 and before every update; three generations are kept in `/var/lib/lightspeak/backups/`. Manually: `sudo lightspeak-admin backup`.
- From time to time, **copy a backup off the server**:
  ```
  sudo ls /var/lib/lightspeak/backups/                                    # find the newest file
  sudo install -m 0600 -o "$USER" /var/lib/lightspeak/backups/<file>.db ~/ls-backup.db
  # on your own computer:
  scp <user>@<app-host>:ls-backup.db .
  ssh <user>@<app-host> rm ls-backup.db
  ```
  The file contains every message and account (passwords only as hashes) – keep it safe.
- Attachments are not part of the backup (to save space). Restoring: [SELF_HOSTING.md → Backups and restore](SELF_HOSTING.md#backups-and-restore).

## Storage

```
sudo lightspeak-admin storage-report              # usage, largest files
sudo lightspeak-admin purge-attachments --days 180
sudo lightspeak-admin vacuum                      # shrink the database
```

To delete old attachments permanently on a schedule, set `LS_RETENTION_DAYS` (see [SELF_HOSTING.md → Settings](SELF_HOSTING.md#settings)).

## Troubleshooting

| Problem | Cause / fix |
|---|---|
| App: “Server unreachable” | `sudo systemctl status lightspeak-server lightspeak-livekit caddy`; logs: `sudo journalctl -u lightspeak-server -n 200 --no-pager` (likewise `-u lightspeak-livekit`, `-u caddy`). |
| Chat works, voice does not | Are 7881/TCP and 7882/UDP open in your provider’s cloud firewall and in ufw? Check the LiveKit log. If it only affects one person: a restrictive firewall or VPN on their side – have them try another network. |
| Certificate errors | Check the Caddy log. Do both host names point at the server’s IP address? With sslip.io you may have hit a Let’s Encrypt rate limit – try again later or use your own domain. |
| Someone cannot hear or be heard | Ask for a diagnostic report (*Settings → About → Diagnostics → Export*). It contains devices, connection state and error messages, but no passwords or message contents. |
| “Waiting for a key from a member …” | A new device or member: another member has to be online once so the call key can be shared. There is deliberately no unencrypted way around this. |
| The app does not update | *Settings → About → Check for updates* shows the reason. The app needs to reach `github.com`; builds made without an update key (self-built) do not update themselves. |
| Disk full / upload rejected | `storage-report`, `purge-attachments`, or raise `LS_STORAGE_QUOTA_GB` in `server.env`. |
