// Push-to-talk and hotkeys inside the browser tab. Browsers offer
// no global hook, so bindings only work while the Lightspeak tab has focus;
// leaving the tab releases everything (no stuck microphone). Same codes and
// events as the native hook (src-tauri/src/input), so settings carry over.
import type { InputConfig, InputEvent } from "../native";
import { keyCodeOf, mouseCodeOf } from "./web";

type Listener = (ev: InputEvent) => void;

const listeners = new Set<Listener>();
const pressed = new Set<number>();
let config: InputConfig | null = null;
let recording = false;
let recorded = new Set<number>();
let active = new Map<string, boolean>();
let pttTimer: ReturnType<typeof setTimeout> | undefined;
let pttOn = false;
let attached = false;

const ESC = 0x1b;

function emit(ev: InputEvent): void {
  for (const l of listeners) l(ev);
}

export function onInput(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function setPtt(on: boolean, delay: number): void {
  clearTimeout(pttTimer);
  if (on) {
    if (!pttOn) emit({ kind: "ptt", active: (pttOn = true) });
  } else if (pttOn) {
    pttTimer = setTimeout(() => emit({ kind: "ptt", active: (pttOn = false) }), delay);
  }
}

function evaluate(): void {
  if (!config) return;
  config.bindings.forEach((b, i) => {
    const id = `${i}`;
    const now = b.keys.length > 0 && b.keys.every((k) => pressed.has(k));
    const was = active.get(id) ?? false;
    if (now === was) return;
    active.set(id, now);
    if (b.action === "pushToTalk") setPtt(now, config!.releaseDelayMs);
    else if (now) emit({ kind: "toggle", action: b.action });
  });
}

function bound(code: number): boolean {
  return recording || !!config?.bindings.some((b) => b.keys.includes(code));
}

function down(code: number): void {
  pressed.add(code);
  if (recording) {
    if (code !== ESC && recorded.size < 4) recorded.add(code);
    return;
  }
  evaluate();
}

function up(code: number): void {
  pressed.delete(code);
  if (recording) {
    if (recorded.size) {
      recording = false;
      emit({ kind: "recorded", keys: [...recorded] });
      recorded = new Set();
    }
    return;
  }
  evaluate();
}

function releaseAll(): void {
  pressed.clear();
  evaluate();
  // Leaving the tab must never leave the microphone open.
  if (pttOn) {
    clearTimeout(pttTimer);
    emit({ kind: "ptt", active: (pttOn = false) });
  }
}

function onKeyDown(e: KeyboardEvent): void {
  const c = keyCodeOf(e);
  if (c === null || e.repeat) return;
  down(c);
}
function onKeyUp(e: KeyboardEvent): void {
  const c = keyCodeOf(e);
  if (c !== null) up(c);
}
function onMouse(e: MouseEvent): void {
  const c = mouseCodeOf(e);
  if (c === null || !bound(c)) return;
  // Back/forward buttons would otherwise navigate away from the app.
  e.preventDefault();
  if (e.type === "mousedown") down(c);
  else if (e.type === "mouseup") up(c);
}
function onVisibility(): void {
  if (document.hidden) releaseAll();
}

function attach(): void {
  if (attached) return;
  attached = true;
  window.addEventListener("keydown", onKeyDown, true);
  window.addEventListener("keyup", onKeyUp, true);
  window.addEventListener("mousedown", onMouse, true);
  window.addEventListener("mouseup", onMouse, true);
  window.addEventListener("auxclick", onMouse, true);
  window.addEventListener("blur", releaseAll);
  document.addEventListener("visibilitychange", onVisibility);
}

function detach(): void {
  if (!attached || config || recording) return;
  attached = false;
  window.removeEventListener("keydown", onKeyDown, true);
  window.removeEventListener("keyup", onKeyUp, true);
  window.removeEventListener("mousedown", onMouse, true);
  window.removeEventListener("mouseup", onMouse, true);
  window.removeEventListener("auxclick", onMouse, true);
  window.removeEventListener("blur", releaseAll);
  document.removeEventListener("visibilitychange", onVisibility);
  pressed.clear();
}

export function inputStart(cfg: InputConfig): void {
  config = cfg;
  active = new Map();
  recording = false;
  attach();
}

export function inputRecord(): void {
  recording = true;
  recorded = new Set();
  pressed.clear();
  attach();
}

export function inputStop(): void {
  config = null;
  recording = false;
  recorded = new Set();
  active = new Map();
  if (pttOn) {
    clearTimeout(pttTimer);
    emit({ kind: "ptt", active: (pttOn = false) });
  }
  detach();
}
