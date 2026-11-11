// Tiny i18n: typed message tables, {placeholder} interpolation, plurals via
// Intl.PluralRules. The locale is reactive, so switching applies instantly.
import { de } from "./de";
import { en, type MessageKey, type Messages } from "./en";
import { DEFAULT_LIMITS, limitParams } from "../limits";

export type Locale = "de" | "en";
export type LocalePref = Locale | "system";
export type { MessageKey };

const tables: Record<Locale, Messages> = { de, en };

export function systemLocale(langs: readonly string[] = navigator.languages ?? [navigator.language]): Locale {
  const first = (langs[0] ?? "en").toLowerCase();
  return first.startsWith("de") ? "de" : "en";
}

let current = $state<Locale>(typeof navigator === "undefined" ? "en" : systemLocale());

export const i18n = {
  get locale(): Locale {
    return current;
  },
  set(pref: LocalePref) {
    current = pref === "system" ? systemLocale() : pref;
    if (typeof document !== "undefined") document.documentElement.lang = current;
  },
};

type Params = Record<string, string | number>;

export function format(s: string, params?: Params): string {
  if (!params) return s;
  return s.replace(/\{(\w+)\}/g, (m, k: string) => (k in params ? String(params[k]) : m));
}

export function t(key: MessageKey, params?: Params): string {
  return format(tables[current][key] ?? en[key] ?? key, params);
}

/** Plural lookup: tp("x", n) reads "x_one"/"x_other" and injects {count}. */
export function tp(base: string, count: number, params?: Params): string {
  const cat = new Intl.PluralRules(current).select(count);
  const key = (`${base}_${cat}` in en ? `${base}_${cat}` : `${base}_other`) as MessageKey;
  return t(key, { count: count.toLocaleString(current), ...params });
}

// Limits named in error messages; the server's values replace them on `ready`.
const errParams: Params = limitParams(DEFAULT_LIMITS);

export function setErrorParams(p: Params): void {
  Object.assign(errParams, p);
}

/** Translates a server error code (falls back to a generic message). */
export function terr(code: string | undefined): string {
  const key = `err.${code}` as MessageKey;
  return key in en ? t(key, errParams) : t("err.unknown");
}

// ---------- formatting ----------

// Plain cache, intentionally not reactive.
const dtfCache = new Map<string, Intl.DateTimeFormat>();
function dtf(opts: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const k = current + JSON.stringify(opts);
  let f = dtfCache.get(k);
  if (!f) {
    f = new Intl.DateTimeFormat(current, opts);
    dtfCache.set(k, f);
  }
  return f;
}

export function formatTime(ms: number): string {
  return dtf({ hour: "2-digit", minute: "2-digit" }).format(ms);
}

export function formatDateTime(ms: number): string {
  return dtf({ dateStyle: "full", timeStyle: "short" }).format(ms);
}

export function formatDay(ms: number): string {
  return dtf({ day: "numeric", month: "long", year: "numeric" }).format(ms);
}

export function formatRelative(ms: number, now = Date.now()): string {
  const rtf = new Intl.RelativeTimeFormat(current, { numeric: "auto" });
  const diff = (ms - now) / 1000;
  const abs = Math.abs(diff);
  if (abs < 60) return rtf.format(Math.round(diff), "second");
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  return rtf.format(Math.round(diff / 86400), "day");
}

export function formatBytes(n: number): string {
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toLocaleString(current, { maximumFractionDigits: i === 0 ? 0 : 1 })} ${units[i]}`;
}
