# Changelog

All notable changes to Lightspeak are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## 1.1.0

### Added

- **Guest access:** an invite can allow guests. Whoever opens such a link joins the space with just a name, without creating an account. A guest stays in that one space and is removed again when they sign out, are removed from the space, or have not been seen for 30 days. Guest invites follow `LS_SIGNUP` like account creation does.

### Changed

- The second field of the invite dialog is now labelled “Can be used by” instead of repeating “Valid for”.
- Updated dependencies, among them LiveKit protocol 1.53.2, SQLite 1.62.0 and Vite 8.3.6.

### Fixed

- A data race in the server when a disconnected session was removed while its resume timer was still pending.

## 1.0.0

First public release.

### Highlights

- **Voice channels** with Opus audio, noise suppression, echo cancellation, per-user volume, voice activation and global push-to-talk (keyboard and mouse buttons).
- **Screen sharing** up to 1440p at 60 fps with hardware-encoded H.264 and the sound of a single app or the whole system.
- **End-to-end encrypted** calls, screen shares and stream audio, with automatic key management.
- **Text chat:** spaces, channels and categories, direct messages and group chats, replies, reactions, pins, mentions, Markdown with syntax highlighting, full-text search and file uploads.
- **Roles and permissions** per space and per channel, private channels, slow mode, an away channel and audio quality presets for voice channels.
- **Windows app** (Tauri) with tray icon, notifications and autostart, and a **browser client** served by your own server.
- **Self-hosting** with a single installer for Debian and Ubuntu (x86_64 and arm64), signed releases, one-command updates with automatic rollback, and daily database backups.
- English and German, light and dark theme.

### Changed

- The Windows app now updates itself from the GitHub releases of the project instead of from the server it is connected to. Updates are still verified against the release key that is built into the app.
- The download link on invite pages points to the latest GitHub release by default (`LS_DOWNLOAD_URL`).
- Reworked wording throughout the settings, in English and German.
