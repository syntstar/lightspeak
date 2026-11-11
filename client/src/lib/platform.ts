// Where the client runs: the Tauri desktop app, the browser build
// served by the Lightspeak server under /app/, or neither (unit tests, plain
// `vite` dev). `WEB_BUILD` is a build-time constant, so the browser-only code
// paths disappear from the desktop bundle and vice versa.

/** True for `vite build --mode web` / `vite --mode web`. */
export const WEB_BUILD = import.meta.env.MODE === "web";

/** True when running inside the Tauri webview (false in unit tests). */
export const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;

/** True for the browser client. */
export const isWeb = WEB_BUILD && !inTauri;

/** Desktop app or browser client: settings, sessions and device keys persist. */
export const hasStore = inTauri || isWeb;
