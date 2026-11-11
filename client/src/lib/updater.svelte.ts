// Client self-update. Checks the signed update manifest on GitHub Releases
// shortly after signing in and then once a day – one timer,
// no other work while idle – and on demand from Settings → About. With
// "install automatically" on, a found update is downloaded in the background
// and installed at the next start, before the window opens (Rust); "Restart
// now" installs it right away. Installing hands over to the Windows
// installer, which restarts Lightspeak afterwards.
import { listen } from "@tauri-apps/api/event";
import { native, inTauri, type UpdateInfo } from "./native";
import { app } from "./state/app.svelte";
import { prefs, savePrefs } from "./prefs.svelte";
import { log } from "./log";

/** After sign-in the network is up; a short delay keeps the start light. */
const FIRST_CHECK_MS = 10_000;
const INTERVAL_MS = 24 * 3600_000;
/** A failed check (offline, server down) is retried sooner than daily. */
const RETRY_MS = 30 * 60_000;

export type UpdateStatus =
  | "idle"
  | "checking"
  | "current"
  | "available"
  /** Downloaded and verified; installs at the next start. */
  | "ready"
  | "installing"
  | "error"
  /** Build without updater key. */
  | "unavailable";

class Updater {
  status = $state<UpdateStatus>("idle");
  info = $state<UpdateInfo | null>(null);
  /** Error code from Rust (e.g. "not_configured"). */
  error = $state<string | null>(null);
  /** Download progress 0..1, null while the size is unknown. */
  progress = $state<number | null>(null);
  /** Version the user postponed in the banner (shown again for newer ones). */
  dismissed = $state<string | null>(null);
  private started = false;
  private retry: ReturnType<typeof setTimeout> | undefined;

  start(): void {
    if (!inTauri || this.started) return;
    this.started = true;
    setTimeout(() => void this.check(), FIRST_CHECK_MS);
    setInterval(() => void this.check(), INTERVAL_MS);
  }

  async check(): Promise<void> {
    if (!inTauri) return;
    if (this.status === "checking" || this.status === "installing") return;
    if (!app.info.updater) {
      this.status = "unavailable";
      this.error = "not_configured";
      return;
    }
    clearTimeout(this.retry);
    this.status = "checking";
    this.error = null;
    try {
      this.info = await native.updateCheck();
      this.status = this.info ? "available" : "current";
    } catch (e) {
      const code = String(e);
      this.error = code;
      this.status = code === "not_configured" ? "unavailable" : "error";
      if (this.status === "error") {
        log("warn", "update check failed:", code);
        this.retry = setTimeout(() => void this.check(), RETRY_MS);
      }
      return;
    }
    if (this.status === "available" && prefs.autoUpdate) await this.prepare();
  }

  /** Background download for the next start; failures keep the banner. */
  private async prepare(): Promise<void> {
    try {
      await native.updateDownload();
      if (this.status === "available") this.status = "ready";
    } catch (e) {
      log("warn", "update download failed:", String(e));
    }
  }

  /** Settings toggle: off drops a prepared download again. */
  async setAuto(on: boolean): Promise<void> {
    prefs.autoUpdate = on;
    savePrefs();
    if (on) {
      if (this.status === "available") await this.prepare();
    } else {
      await native.updateDiscard().catch(() => {});
      if (this.status === "ready") this.status = "available";
    }
  }

  async install(): Promise<void> {
    if (this.status !== "available" && this.status !== "ready") return;
    const prepared = this.status === "ready";
    this.status = "installing";
    this.progress = prepared ? 1 : 0;
    const unlisten = await listen<{ done: number; total: number | null }>("update-progress", (e) => {
      this.progress = e.payload.total ? Math.min(1, e.payload.done / e.payload.total) : null;
    });
    try {
      await native.updateInstall(); // the app exits here on success
    } catch (e) {
      this.status = "error";
      this.error = String(e);
      log("warn", "update install failed:", this.error);
    } finally {
      unlisten();
    }
  }

  get bannerVisible(): boolean {
    return (
      (this.status === "available" || this.status === "ready") &&
      !!this.info &&
      this.dismissed !== this.info.version
    );
  }
}

export const updater = new Updater();
