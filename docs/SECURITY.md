# Security

How Lightspeak protects your conversations, and where its limits are. To report a vulnerability, see the [security policy](../.github/SECURITY.md).

## Threat model

Lightspeak is made for private groups that host their own server. It is designed against:

1. Strangers on the internet (scans, brute force, exploits against open services).
2. Intercepted connections (public Wi-Fi).
3. A compromised or curious server → **calls and screen shares are end-to-end encrypted**.
4. Malicious content in messages and files (XSS in the webview, crafted images).
5. A stolen device or app data read by other software.

**Out of scope:**

- **Text messages and files are not end-to-end encrypted.** They are protected by TLS in transit and stored in plain form on the server. Whoever operates the server can read them.
- **Private channels** are an access boundary enforced by the server, not a cryptographic one. Text and files are stored like everywhere else, and voice uses the media key of the space, which every member of the space holds. The protection is that the server only delivers content, events and LiveKit tokens to people who are allowed to see the channel.
- Metadata (who talks to whom, when, in which channel) is visible to the server.

## Transport

- HTTPS/WSS only, through Caddy (Let’s Encrypt), with HSTS and TLS 1.2 or newer. Plain HTTP is only used for the ACME challenge and the redirect.
- WebRTC media is always DTLS-SRTP, with end-to-end encryption on top.
- The app requires HTTPS for every server except `localhost`/`127.0.0.1` and makes no certificate exceptions.

## Authentication and sessions

- Registration needs a valid invite. Passwords have at least 10 characters and are checked against a list of common passwords.
- Password hashes: **Argon2id** (64 MiB, 3 passes), with a constant-time path for unknown usernames and a cap on parallel hashes.
- Session tokens are 32 random bytes; the database only stores their SHA-256 hash. Sessions expire after 90 days without use and can be revoked individually.
- The Windows app keeps the token and the device’s private key in the **Windows Credential Manager**, separately for each server. No server ever sees another server’s token.
- **Browser client:** the session is a cookie (`__Host-` prefix, `HttpOnly`, `Secure`, `SameSite=Strict`), so scripts can never read it. The server only accepts the cookie together with a custom request header and a matching `Origin`, and rejects cross-site sign-in attempts. The WebSocket gateway accepts it only for upgrade requests from the server’s own origin.
- Rate limits (per IP address and per account) apply to sign-in, registration, uploads, messages and WebSocket connections. They are deliberately not configurable.

## Authorisation

- Every REST route and every WebSocket event checks membership, role and channel permissions on the server. The client only hides what a user may not do; it is never trusted. Resources the user may not see answer with 404.
- LiveKit tokens are issued only by the server, are valid for 10 minutes and for one room, and carry only the sources the user may publish (for example no microphone in a channel where they may not speak).
- LiveKit webhooks are signature-checked. Kicks and bans also remove the person from running calls.
- `LS_INVITES=admins` is enforced both when an invite is created and every time one is used.

## End-to-end encryption (calls and screen shares)

Goal: the server – and anything else running on the same machine – cannot listen to voice, screen video or stream audio. Key management is automatic; there is no password to share.

### Keys

- **Device key:** every device creates an X25519 key pair on first sign-in. The private key stays in the Credential Manager; the public key is registered with the server.
- **Browser:** every browser is a device of its own. The key pair is created with WebCrypto as a **non-exportable** key and stored in IndexedDB; scripts can use it to open envelopes but cannot read the key bytes. Without X25519 or encoded-transform support the browser client refuses calls – there is no unencrypted fallback.
- **Media key:** one random 32-byte key per *container* (a space, a DM or a group DM), with an epoch number.
- **Envelopes:** the media key is encrypted for every member device with a sealed box (X25519 + XSalsa20-Poly1305, compatible with libsodium’s `crypto_box_seal`) and stored on the server. The server only ever sees ciphertext.

### Flows

- **New container:** the creator’s client generates epoch 1 and the envelopes for its own devices.
- **New member or new device:** the server asks members who are online; the first client that holds the key creates the missing envelopes. Until then the new client shows “Waiting for a key from a member …”. If no device with an envelope is online, the server requests a rotation instead (a new epoch, sealed for all current member devices).
- **Member removed or banned, device signed out:** a remaining client creates the next epoch with envelopes for the remaining devices. Running calls switch to the new key.
- **Joining a call:** the client fetches its envelope for the current epoch, opens it natively and hands the raw key to LiveKit’s end-to-end encryption worker. Keys never touch `localStorage` or logs.

### Trust and limits

- The interface shows a lock in the voice panel. If encryption cannot be set up, joining is aborted with a clear message.
- Device keys are distributed by the server. A server that is actively malicious – not merely curious – could register an additional device for an account and would then receive envelopes like any other new device. Lightspeak protects against passive access to the server (backups, logs, other software on the machine, a network observer), not against an operator who modifies the server to attack their own users. To check a contact’s device key yourself, compare the **safety number** under *Settings → Privacy & security* in person or over another channel.
- Supported codecs with E2EE: Opus, VP8 and H.264.

## Client hardening

- **Strict content security policy:** `script-src 'self'` (plus WebAssembly), no frames, no objects. Message rendering never inserts user content as HTML.
- **Markdown** is rendered by a small, linear parser that only creates text nodes and a fixed whitelist of elements. The output of the syntax highlighter is parsed in an inert document and rebuilt from text nodes and `<span class>` only. Text attachments are shown only on click, size-limited and as plain text.
- **Links:** only `http(s)` and `mailto`, opened in the default browser, with a confirmation that shows the full address for foreign domains. Bidirectional control characters are neutralised.
- **Credential warning:** before sending or attaching, the client checks locally for well-known token formats, private keys and files such as `.env`, and asks before sending. Nothing is logged or transmitted.
- **Choice of server:** invite links to a different server are shown and must be confirmed. The webview does not navigate away from the app and loads no remote content.
- **Tauri capabilities** are minimal: only the commands and plugins that are needed, no shell execution, no generic file-system access.
- **Deep links:** only `lightspeak://invite/…` with a strict pattern is accepted.
- **Updates:** the app downloads updates from the project’s GitHub releases and installs them only if they are signed with the release key that is compiled into the app. The signed version must match the announced one, so an old release cannot be passed off as new. Trust rests on the signature, not on GitHub.
- Logs and diagnostic reports contain no tokens, keys or message contents.

### Browser client

- Served by your own server under `/app/` (same origin as the API, no CORS). Its content security policy names the server’s own hosts only, without wildcards. A `Permissions-Policy` allows just the microphone and screen capture; `Cross-Origin-Opener-Policy` is `same-origin`.
- The invite code travels in the URL fragment (it never reaches the server logs) and is removed from the address immediately.
- Invites to other servers open that server’s invite page in a new tab; the browser client never talks to a server other than its own.

## Server hardening

- Services run as their own system users with systemd sandboxing (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, an empty capability set, memory limits). Caddy only gets `CAP_NET_BIND_SERVICE`.
- Only the required ports are public: 80 and 443 (TCP, and UDP 443 for HTTP/3), 7881/TCP and 7882/UDP. The Lightspeak server, the LiveKit API and Caddy’s admin interface are only reachable locally.
- Uploads: size and quota checks, the file name is metadata only, files are stored under random IDs, served with `X-Content-Type-Options: nosniff`, a sandboxing content security policy and as attachments. Images are decoded with a pixel limit to defuse decompression bombs.
- File links are HMAC-signed URLs that are valid for about a day. Whoever has such a link can download the file during that time.
- Secrets (LiveKit API key and secret, the signing key for file URLs) are generated by the installer on the server and stored in `/etc/lightspeak/*.env`, readable by root and the service only.
- SQLite is accessed with prepared statements only.

## Releases

- `install.sh --version` and `lightspeak-admin update` only accept files that are listed in a signed `SHA256SUMS`. The signature uses the same offline key as the app updates (minisign: Ed25519 over BLAKE2b-512) and is verified with `openssl` alone. There is no way around the signature, so a compromised GitHub account or mirror is not enough to slip in server code.
- A signature proves origin, not freshness. Therefore the version inside the binary must match the requested one, and `latest` never installs anything older than what is running. An explicit downgrade asks first.
- The optional deploy account may run `lightspeak-admin`, restart the services and read their logs as root, and nothing else. `update` only takes `--version` and `--yes`, never file paths, and `reconfigure` takes no arguments. Treat the account as an admin of the Lightspeak installation and protect its SSH key accordingly. Releases are staged in a root-owned directory, and server binaries are only ever executed as the unprivileged service user, even while being verified.
- Dependencies are pinned. `govulncheck`, `cargo audit` and `npm audit --omit=dev` are part of the release process.

## Known trade-offs

- The sign-in rate limit per username lets a stranger slow down sign-ins for a name they know. Sessions last 90 days, so the impact is small.
- The Windows installer is not code-signed with a commercial certificate, so Windows SmartScreen warns on first install. Verify the download with `SHA256SUMS` from the release.
