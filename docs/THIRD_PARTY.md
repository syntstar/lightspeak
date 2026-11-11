# Third-party components

Lightspeak itself is licensed under [AGPL-3.0](../LICENSE). It builds on the following libraries and assets, each under its own licence. Transitive dependencies are listed in `client/package-lock.json`, `client/src-tauri/Cargo.lock` and `server/go.sum`.

Rule for contributions: every library, asset or code snippet that is added gets an entry here. Accepted licences are those compatible with AGPL-3.0, in practice MIT, Apache-2.0, BSD, ISC, Zlib, MPL-2.0 and OFL (fonts). Code without a licence is not accepted.

## Apps (Windows app and browser client)

| Component | Version | Licence | Source | Used for |
|---|---|---|---|---|
| Tauri | 2.12.5 | MIT / Apache-2.0 | https://github.com/tauri-apps/tauri | Application shell |
| @tauri-apps/api, plugin-opener | 2.12.4 / 2.7.2 | MIT / Apache-2.0 | https://github.com/tauri-apps | Bridge to the native side, opening links |
| tauri-plugin-updater | 2.14.1 | MIT / Apache-2.0 | https://github.com/tauri-apps/plugins-workspace | Signed self-update |
| tauri-plugin-single-instance | 2.5.5 | MIT / Apache-2.0 | https://github.com/tauri-apps/plugins-workspace | Single instance, invite links |
| Svelte | 5.57.3 | MIT | https://github.com/sveltejs/svelte | User interface |
| livekit-client | 2.23.0 | Apache-2.0 | https://github.com/livekit/client-sdk-js | Voice, screen sharing, E2EE |
| highlight.js | 11.12.0 | BSD-3-Clause | https://github.com/highlightjs/highlight.js | Syntax highlighting in code blocks (loaded on demand) |
| @sapphi-red/web-noise-suppressor | 0.4.1 | MIT | https://github.com/sapphi-red/web-noise-suppressor | Noise suppression “Strong” (RNNoise worklet, loaded on demand) |
| RNNoise (included in the above, via shiguredo/rnnoise-wasm) | – | BSD-3-Clause (xiph/rnnoise) / Apache-2.0 (rnnoise-wasm) | https://github.com/xiph/rnnoise | WASM model in the worklet |
| @noble/ciphers | 2.4.0 | MIT | https://github.com/paulmillr/noble-ciphers | Browser client: XSalsa20-Poly1305 / HSalsa20 (sealed box) |
| @noble/hashes | 2.4.0 | MIT | https://github.com/paulmillr/noble-hashes | Browser client: BLAKE2b (sealed-box nonce) |
| Inter (@fontsource-variable/inter) | 5.3.0 | OFL-1.1 | https://github.com/rsms/inter | Interface font (Latin and Latin Extended, embedded) |
| Lucide (@lucide/svelte) | 1.66.0 | ISC | https://github.com/lucide-icons/lucide | Icons (imported individually) |
| windows (crate) | 0.62.2 | MIT / Apache-2.0 | https://github.com/microsoft/windows-rs | Hooks, windows, audio |
| wasapi (crate) | 0.26.0 | MIT | https://github.com/HEnquist/wasapi-rs | Process loopback capture |
| webview2-com | 0.39.1 | MIT | https://github.com/wravery/webview2-rs | WebView2 memory target |
| crypto_box | 0.9.1 | MIT / Apache-2.0 | https://github.com/RustCrypto/nacl-compat | X25519 device keys, sealed-box envelopes (E2EE) |
| base64 (crate) | 0.22 | MIT / Apache-2.0 | https://github.com/marshallpierce/rust-base64 | Key encoding |
| serde, serde_json | 1 | MIT / Apache-2.0 | https://github.com/serde-rs | Serialisation |

## Server

| Component | Version | Licence | Source | Used for |
|---|---|---|---|---|
| LiveKit Server | 1.13.11 | Apache-2.0 | https://github.com/livekit/livekit | Media server (SFU); downloaded by the installer, not bundled |
| Caddy | distribution package | Apache-2.0 | https://github.com/caddyserver/caddy | Reverse proxy and HTTPS; installed by the installer, not bundled |
| github.com/livekit/protocol | 1.53.1 | Apache-2.0 | https://github.com/livekit/protocol | Access tokens, RoomService client |
| github.com/twitchtv/twirp | 8.1.3 | Apache-2.0 | https://github.com/twitchtv/twirp | RoomService transport |
| google.golang.org/protobuf | 1.36.12 | BSD-3-Clause | https://github.com/protocolbuffers/protobuf-go | Webhook JSON |
| github.com/go-chi/chi/v5 | 5.5.0 | MIT | https://github.com/go-chi/chi | HTTP router |
| github.com/coder/websocket | 1.8.15 | ISC | https://github.com/coder/websocket | WebSocket gateway |
| modernc.org/sqlite | 1.61.1 | BSD-3-Clause | https://gitlab.com/cznic/sqlite | SQLite without CGO |
| golang.org/x/crypto | 0.58.0 | BSD-3-Clause | https://go.googlesource.com/crypto | Argon2id |
| golang.org/x/image | 0.47.0 | BSD-3-Clause | https://go.googlesource.com/image | WebP decoder, image scaling |

Discord and TeamSpeak are trademarks of their respective owners. Lightspeak is not affiliated with them.
