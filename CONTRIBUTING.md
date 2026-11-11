# Contributing to Lightspeak

Thanks for your interest! Bug reports, ideas and pull requests are welcome.

- **Bugs and ideas:** open an [issue](https://github.com/syntstar/lightspeak/issues). For bugs, a diagnostic report helps a lot (*Settings → About → Diagnostics → Export*; it contains no passwords or message contents).
- **Security problems:** please do not open a public issue – see the [security policy](.github/SECURITY.md).
- **Larger changes:** open an issue first so we can agree on the direction before you invest time.

## Principles

When goals conflict, this order decides:

1. **Security** – no secrets in the repository or in logs; everything is authorised on the server; no unencrypted fallback for end-to-end encrypted calls.
2. **Voice must always work** – reconnects, device changes and error states are part of every feature.
3. **Resources** – what is not used must not cost anything. Memory and CPU regressions are bugs.
4. **Usability** – understandable without a manual, fully translated (English and German), usable with the keyboard, in light and dark mode.
5. **Simplicity** – the smallest solution that meets the requirement. No new dependency without a good reason.

## Project layout

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). In short: `client/` is the Windows app (Tauri 2, Rust + Svelte 5) and the browser client, `server/` is the Go backend, `deploy/install.sh` installs and updates a Linux server.

## Building

### Prerequisites

- **Server:** [Go](https://go.dev/dl/) 1.27 or newer. Works on Windows, Linux and macOS.
- **Apps:** [Node.js](https://nodejs.org) 22 or newer. For the Windows app also [Rust](https://rustup.rs) (stable) and the [Tauri prerequisites for Windows](https://tauri.app/start/prerequisites/) (Microsoft C++ Build Tools, WebView2).
- **Voice locally:** a [livekit-server](https://github.com/livekit/livekit/releases) binary.

### Run everything locally

```bash
# 1. LiveKit in dev mode
livekit-server --dev --bind 127.0.0.1

# 2. Server (port 8080, data in server/dev-data)
cd server
go run ./cmd/lightspeak-server serve --dev
go run ./cmd/lightspeak-server admin --dev bootstrap   # prints an owner invite

# 3. Windows app
cd client
npm install
npm run tauri dev
```

In the app, enter `127.0.0.1:8080` as the server and use the invite code.

A second instance with its own data, for testing with two accounts: `npm run tauri dev -- -- --profile test2`.

The browser client is built with `npm run build:web` (output in `client/dist-web`). The server serves it under `/app/` from a folder named `web` next to its binary.

### Checks

Please run these before opening a pull request – CI runs the same.

```bash
# Server
cd server
go vet ./...
go test ./...          # add -race where CGO is available
staticcheck ./...      # go install honnef.co/go/tools/cmd/staticcheck@latest

# Apps
cd client
npm run check          # types (svelte-check)
npm run lint           # eslint + prettier
npm test               # unit tests (vitest)

# Rust core (Windows)
cd client/src-tauri
cargo clippy --all-targets -- -D warnings
cargo test
```

`npm run format` and `cargo fmt` fix formatting.

## Conventions

- **Language:** code, comments, commit messages and documentation are in English.
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org) (`feat:`, `fix:`, `docs:`, `refactor:` …), one topic per commit.
- **Interface texts** never live in components. Add a key to `client/src/lib/i18n/en.ts` (the reference) **and** `de.ts`; the type checker reports missing keys.
- **Authorisation** is always enforced on the server, and every new route gets a test that proves a stranger is rejected.
- **Tests** accompany behaviour changes: Go tests next to the code, `*.test.ts` for client logic.
- **Dependencies:** pin versions, and add every new library to [docs/THIRD_PARTY.md](docs/THIRD_PARTY.md) with its licence.
- **Never trust message content:** user text is rendered as text nodes only, never as HTML.

### Definition of done

Works in English and German · light and dark · error and offline states handled · usable with the keyboard · tests added (server: authorisation always) · no noticeable memory or CPU regression · no secrets.

To measure the app’s memory and CPU including its WebView2 processes:

```powershell
./scripts/measure-memory.ps1 -Name Lightspeak -Seconds 30 -Label idle
```

## Licence of contributions

Lightspeak is licensed under [AGPL-3.0](LICENSE). By submitting a contribution you agree that it is published under the same licence.

## Releases

This part is for maintainers – and for anyone who runs their own fork.

A release consists of the Windows installer, the server binaries, the browser client, `install.sh`, the update manifest `latest.json` and a signed `SHA256SUMS`. Apps check `https://github.com/<owner>/<repo>/releases/latest/download/latest.json`, servers fetch the same release with `lightspeak-admin update`.

```powershell
./scripts/bump-version.ps1 1.2.3     # sets the version everywhere
# add a "## 1.2.3" section to CHANGELOG.md, commit, push
./scripts/release.ps1                # checks, builds and signs into dist/release/1.2.3
./scripts/release.ps1 -Publish       # the same, then publishes the GitHub release (needs gh)
```

The release notes are taken from the matching section of `CHANGELOG.md`.

### The release key

Apps and servers only install what is signed with the release key. The public half is in the repository (`client/src-tauri/updater.pub` and `RELEASE_PUBKEY_DEFAULT` in `deploy/install.sh`); the private half exists only on the maintainer’s machine.

`release.ps1` reads the private key from `TAURI_SIGNING_PRIVATE_KEY` (a path or the key itself) or from `%USERPROFILE%\.lightspeak\updater.key`, and asks for its password unless `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` is set. Nothing is stored.

**The private key and its password must never be committed, uploaded or shared.** `.gitignore` excludes `*.key`, but do not keep the key inside the working tree at all.

### Your own fork

A fork cannot sign with the project’s key, so it needs its own:

```powershell
cd client
npx tauri signer generate -w $env:USERPROFILE\.lightspeak\updater.key
```

1. Replace `client/src-tauri/updater.pub` with the generated `updater.key.pub` and set `RELEASE_PUBKEY_DEFAULT` in `deploy/install.sh` to the same value (`release.ps1` refuses to build if they differ).
2. Build the app with `LIGHTSPEAK_UPDATE_URL` pointing at your manifest, for example `https://github.com/<you>/<repo>/releases/latest/download/latest.json`, and pass `-Repo <you>/<repo>` to `release.ps1`.
3. Point servers at your releases with `LS_RELEASE_URL` and `LS_RELEASE_PUBKEY`.

Keep the key safe (password manager plus an offline copy). If it is lost, existing installations can no longer update automatically and everyone has to reinstall once by hand. If it is stolen, create a new key immediately and ask everyone to reinstall.

Without `updater.pub` the app builds fine but has no self-update, and the interface says so.
