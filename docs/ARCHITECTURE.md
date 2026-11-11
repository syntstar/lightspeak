# Architecture

A short tour of how Lightspeak is put together. For the security design see [SECURITY.md](SECURITY.md).

## Overview

```
 Windows PC (each user)                                  Your server (Linux, systemd)
┌───────────────────────────────────────────┐         ┌────────────────────────────────────────────┐
│ Lightspeak.exe  (Tauri 2, Rust)           │         │ Caddy  :80/:443  (TLS, Let's Encrypt)      │
│  ├─ Rust core                             │  HTTPS  │   ├─ /api/*, /ws  → lightspeak-server :8080│
│  │   • push-to-talk hook (keyboard/mouse) │◄───────►│   ├─ /app/*       → browser client         │
│  │   • WASAPI loopback (app/system audio) │  WSS    │   ├─ /invite/*    → invite page            │
│  │   • window list + process lookup       │         │   └─ rtc host     → livekit-server   :7880 │
│  │   • E2EE device keys (X25519)          │         │                                            │
│  │   • Credential Manager (token, keys)   │         │ lightspeak-server (Go, one binary, SQLite) │
│  │   • tray, toasts, idle, updater        │         │   auth · spaces · channels · messages      │
│  │   • WebView2 memory mode               │         │   files · presence · invites · LiveKit     │
│  └─ WebView2 (one single webview)         │         │   tokens                                   │
│      • Svelte 5 UI                        │  WebRTC │                                            │
│      • livekit-client (voice/screen/E2EE) │◄───────►│ livekit-server (Go, one binary)            │
│      • AudioWorklet for native stream     │ (SRTP)  │   UDP 7882 (mux) · TCP 7881 (fallback)     │
│        audio                              │         │   webhooks → lightspeak-server             │
└───────────────────────────────────────────┘         └────────────────────────────────────────────┘
```

All server components run as **systemd services without Docker**, each under its own unprivileged user. `deploy/install.sh` sets them up; see [SELF_HOSTING.md](SELF_HOSTING.md).

The app has no built-in server address. Each installation of the server has its own host name, and the app learns it from the invite link.

## Technology choices

| Area | Choice | Why |
|---|---|---|
| Desktop shell | **Tauri 2** (Rust) | Uses the WebView2 runtime that Windows already ships; tiny installer; native access to Windows APIs |
| UI | **Svelte 5 + TypeScript + Vite**, hand-written CSS with custom properties | Smallest sensible runtime, no UI framework |
| Lists | Virtualised message list | Only visible messages are in the DOM |
| Icons / font | **Lucide** (imported individually) / **Inter Variable** (subset, woff2) | Small, permissively licensed |
| Media | **LiveKit** (SFU) + **livekit-client** | Proven: Opus, RED, DTX, simulcast, ICE/TCP fallback, E2EE, reconnects |
| Screen-share codec | **H.264** (hardware encoder via Media Foundation), fallback VP8 | Hardware encoding on AMD/NVIDIA/Intel, compatible with E2EE |
| Stream audio | **Native WASAPI process loopback** → Tauri channel → AudioWorklet → LiveKit track | The only way to capture “just this app” or “the system without my own voice chat” |
| Push-to-talk | **Low-level hook in Rust** | Keyboard and mouse buttons, real key-up events, only installed while needed |
| Backend | **Go** (chi, coder/websocket, modernc.org/sqlite, LiveKit protocol, x/crypto/argon2) | 20–40 MB of RAM, no CGO, cross-compiles statically from any OS |
| Database | **SQLite** (WAL, FTS5) | Zero operations effort, ideal for small groups |
| Reverse proxy | **Caddy** | Automatic HTTPS, minimal configuration |
| Deployment | Static binaries + systemd, **no Docker** | Low memory and disk use, works on a Raspberry Pi |

## Client resource model

The basic rule: **what is not used costs nothing.**

| State | Active |
|---|---|
| Background / tray, no call | Rust core + one WebView2 (memory target level **Low**), one WebSocket, no animations, no hook |
| Window open, text chat | + DOM for the visible messages (virtualised), images as server-side thumbnails |
| In a voice channel | + LiveKit room (audio tracks only), audio levels only for visible speaker indicators |
| Watching a stream | + the video track is subscribed **only now** and unsubscribed again when the stream is closed |
| Sharing your screen | + `getDisplayMedia` (hardware H.264) + optional native loopback capture |
| Push-to-talk during a call | + low-level hook thread (removed when you leave the call) |

Loaded on demand (dynamic import): settings, the E2EE worker, the RNNoise WASM module, emoji data, syntax highlighting, the lightbox and the stream dialog.

## Data flows

### Sign-in and sessions

1. Registration needs an invite code → `POST /api/v1/auth/register` (Argon2id).
2. The server returns an opaque session token (256 bit; only its SHA-256 hash is stored). The app keeps it in the **Windows Credential Manager**, never in `localStorage`.
3. On first sign-in the device creates an **X25519 key pair** (private key in the Credential Manager) and registers the public key.
4. **Browser client:** the same Svelte code as a second build target, served by the server under `/app/`. Instead of the Credential Manager, the session is an HttpOnly cookie (scripts never see the token) and the device key is a non-exportable WebCrypto X25519 key in IndexedDB. Instead of the Rust core there is `client/src/lib/web/*`. `lib/native.ts` picks the implementation at run time; the desktop bundle does not contain the web code.

### Real time

- One WebSocket connection `wss://…/ws` per client (JSON events, sequence numbers, resume after a drop, heartbeat every 30 s).
- REST for everything that needs a response (pagination, uploads).

### Voice

1. Client: `POST /api/v1/voice/join {channelId}` → the server checks membership, permissions and limits and issues a LiveKit token (valid for 10 minutes, this room only, only the sources the user may publish).
2. The client connects to LiveKit through Caddy (signalling) and via UDP 7882 or TCP 7881 (media).
3. LiveKit webhooks → server → `voice_state_update` to all members (who is in which channel, muted, deafened, streaming). The server reconciles with LiveKit every 60 s.
4. E2EE: the media key of the container (space or DM) is decrypted locally and handed to the LiveKit key provider (see [SECURITY.md](SECURITY.md)).

### Screen sharing

1. The app’s own dialog: mode (screen/window), quality (720p/1080p/1440p/native; 15/30/60 fps), content type (auto/game or video/text), audio (off/on).
2. `getDisplayMedia` opens the system picker for the concrete source.
3. The Rust core maps the chosen window to a process (by window handle or title; if that is ambiguous, the user picks from the apps that are currently playing audio).
4. Audio:
   - **Window** → WASAPI process loopback **including** the app’s process tree.
   - **Screen** → WASAPI process loopback **excluding** Lightspeak’s own process tree (all system audio without the voices of the call, so viewers hear no echo).
5. Rust sends PCM (48 kHz, stereo, f32) over a Tauri channel → AudioWorklet with a ring buffer → published as a screen-share audio track (Opus stereo).
6. Video is published as H.264 with simulcast (a small preview layer for tiles); unused layers are paused.

In the browser client, steps 3–5 are replaced by the browser’s own tab or system audio capture.

### Files

- Uploads are streamed to `POST /api/v1/channels/{id}/attachments` (quota checked before and after). The file is stored on disk under a random ID; images get a server-side thumbnail.
- Downloads use short-lived signed URLs (`/files/{id}?exp=…&sig=…`) and are always served as attachments, except image thumbnails.

## Protocol in brief

- **REST:** base `https://<host>/api/v1`, JSON in `camelCase`, IDs as strings, `Authorization: Bearer <token>` (browser client: session cookie plus the header `X-Lightspeak-Client: web`). Errors look like `{"error": {"code": "invite_expired", "message": "…"}}`; the client translates `code`, `message` is for logs only.
- **WebSocket:** frames are `{"op": "<name>", "d": {…}, "s": <seq>}`. The client sends `identify` and receives `ready` with a full snapshot (spaces, channels, DMs, read states, presences, voice states), or `resumed` plus the events it missed. After that the server pushes events such as `message_create`, `channel_update`, `presence_update` or `voice_state_update` – only to clients that are allowed to see them.
- **Versioning:** `GET /api/v1/info` reports `protocolVersion` and `minClientVersion`. The app asks the user to update when it is older than the server requires.
- **Invite links:** `https://<host>/invite/<code>` is a small landing page with a download button, “Open in Lightspeak” (`lightspeak://invite/<host>/<code>`) and “Open in browser” (`/app/#invite=<code>`).

The routes are registered in `server/internal/server/server.go`; the shared types live in `server/internal/server/models.go` and `client/src/lib/api/`.

## Repository layout

```
├─ client/                  Windows app and browser client
│  ├─ src/                  Svelte UI
│  │  ├─ lib/api/           REST and WebSocket client, types
│  │  ├─ lib/voice/         LiveKit integration, microphone pipeline, E2EE, stream audio worklet
│  │  ├─ lib/state/         app state (Svelte 5 runes)
│  │  ├─ lib/ui/            base components (button, toggle, slider, menu, sheet …)
│  │  ├─ lib/i18n/          en.ts (reference), de.ts, typed t() helper
│  │  ├─ lib/web/           browser replacements for the native parts
│  │  ├─ views/             screens: chat, voice, stream, settings …
│  │  └─ styles/            design tokens, base styles
│  └─ src-tauri/            Rust core
│     ├─ src/audio/         WASAPI process loopback, audio sessions
│     ├─ src/input/         push-to-talk hook
│     ├─ src/platform/      Credential Manager, toasts, taskbar, registry
│     ├─ src/crypto.rs      device keys, sealed-box envelopes
│     ├─ src/update.rs      signed self-update from GitHub Releases
│     └─ capabilities/      Tauri permissions (minimal)
├─ server/                  Go backend
│  ├─ cmd/lightspeak-server serve, admin and maintenance subcommands
│  └─ internal/             auth, config, ratelimit, server (HTTP, gateway, voice, files), store (SQLite + migrations)
├─ deploy/                  install.sh (installer and updater for Linux servers), server.env.example
├─ scripts/                 release.ps1, bump-version.ps1, measure-memory.ps1
└─ docs/                    user, hosting and admin guides, this document
```
