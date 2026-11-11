// Local diagnostics log (Settings → About → Open logs). Warnings, errors and
// uncaught exceptions are appended to a capped file in the profile folder;
// the Rust side scrubs tokens/keys. Rate-limited so a loop cannot fill it.
import { native, inTauri } from "./native";

const MAX_PER_MINUTE = 60;
let windowStart = 0;
// Separate budgets: a flood of library warnings must not drown our own
// diagnostics (info) and vice versa.
const counts = { info: 0, warn: 0, error: 0 };

function fmt(args: unknown[]): string {
  return args
    .map((a) => {
      if (a instanceof Error) return `${a.name}: ${a.message}${a.stack ? `\n${a.stack}` : ""}`;
      if (typeof a === "string") return a;
      try {
        return JSON.stringify(a);
      } catch {
        return String(a);
      }
    })
    .join(" ");
}

export function log(level: "info" | "warn" | "error", ...args: unknown[]): void {
  if (!inTauri) return;
  const now = Date.now();
  if (now - windowStart > 60_000) {
    windowStart = now;
    counts.info = counts.warn = counts.error = 0;
  }
  if (++counts[level] > MAX_PER_MINUTE) return;
  void native.logWrite(level, fmt(args)).catch(() => {});
}

/** Mirrors console.warn/error and uncaught errors into the log file. */
export function initLog(): void {
  if (!inTauri) return;
  for (const level of ["warn", "error"] as const) {
    const orig = console[level].bind(console);
    console[level] = (...args: unknown[]) => {
      orig(...args);
      log(level, ...args);
    };
  }
  window.addEventListener("error", (e) => log("error", "uncaught", e.error ?? e.message));
  window.addEventListener("unhandledrejection", (e) => log("error", "unhandled rejection", e.reason));
}
