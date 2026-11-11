// UI sounds (join/leave/mute/unmute/ringing), synthesised with WebAudio:
// no sound files, and the AudioContext is closed again shortly after the
// last sound so nothing runs while idle.
import { doNotDisturb } from "../notifier.svelte";
import { prefs } from "../prefs.svelte";

export type Sound =
  | "join"
  | "leave"
  | "mute"
  | "unmute"
  | "deafen"
  | "undeafen"
  | "peerJoin"
  | "peerLeave"
  | "streamStart"
  | "streamStop";

let ctx: AudioContext | null = null;
let idleTimer: ReturnType<typeof setTimeout> | undefined;

function context(): AudioContext {
  clearTimeout(idleTimer);
  if (!ctx || ctx.state === "closed") {
    ctx = new AudioContext({ latencyHint: "interactive" });
    const sink = prefs.audio.outputDevice;
    const c = ctx as AudioContext & { setSinkId?: (id: string) => Promise<void> };
    if (sink && c.setSinkId) void c.setSinkId(sink).catch(() => {});
  }
  return ctx;
}

function releaseLater(ms = 4000): void {
  clearTimeout(idleTimer);
  idleTimer = setTimeout(() => {
    void ctx?.close().catch(() => {});
    ctx = null;
  }, ms);
}

/** One soft sine "pling" with a short attack and exponential decay. */
function tone(c: AudioContext, freq: number, at: number, dur: number, gain: number): void {
  const o = c.createOscillator();
  const g = c.createGain();
  o.type = "sine";
  o.frequency.setValueAtTime(freq, at);
  g.gain.setValueAtTime(0, at);
  g.gain.linearRampToValueAtTime(gain, at + 0.012);
  g.gain.exponentialRampToValueAtTime(0.0001, at + dur);
  o.connect(g).connect(c.destination);
  o.start(at);
  o.stop(at + dur + 0.02);
}

const PATTERNS: Record<Sound, [freq: number, offset: number, dur: number][]> = {
  join: [
    [660, 0, 0.16],
    [990, 0.09, 0.22],
  ],
  leave: [
    [880, 0, 0.16],
    [587, 0.09, 0.24],
  ],
  peerJoin: [
    [740, 0, 0.12],
    [988, 0.07, 0.16],
  ],
  peerLeave: [
    [784, 0, 0.12],
    [523, 0.07, 0.18],
  ],
  mute: [[440, 0, 0.12]],
  unmute: [[660, 0, 0.12]],
  deafen: [
    [523, 0, 0.1],
    [392, 0.07, 0.14],
  ],
  undeafen: [
    [392, 0, 0.1],
    [523, 0.07, 0.14],
  ],
  streamStart: [
    [523, 0, 0.1],
    [784, 0.07, 0.1],
    [1047, 0.14, 0.18],
  ],
  streamStop: [
    [1047, 0, 0.1],
    [784, 0.07, 0.1],
    [523, 0.14, 0.18],
  ],
};

function allowed(): boolean {
  return prefs.audio.sounds && !doNotDisturb();
}

export function play(s: Sound): void {
  if (!allowed()) return;
  const c = context();
  const now = c.currentTime + 0.02;
  const vol = 0.12 * Math.min(2, prefs.audio.outputVolume / 100);
  for (const [f, off, dur] of PATTERNS[s]) tone(c, f, now + off, dur, vol);
  releaseLater();
}

/** Ringtone for incoming/outgoing calls until stop() is called. */
export function ring(outgoing = false): () => void {
  if (!allowed()) return () => {};
  let stopped = false;
  const burst = () => {
    if (stopped) return;
    const c = context();
    const now = c.currentTime + 0.02;
    const vol = (outgoing ? 0.06 : 0.12) * Math.min(2, prefs.audio.outputVolume / 100);
    const notes = outgoing
      ? [[523, 0, 0.5]]
      : [
          [784, 0, 0.18],
          [988, 0.2, 0.18],
          [784, 0.4, 0.18],
          [988, 0.6, 0.3],
        ];
    for (const [f, off, dur] of notes) tone(c, f!, now + off!, dur!, vol);
  };
  burst();
  const iv = setInterval(burst, outgoing ? 3000 : 2200);
  return () => {
    stopped = true;
    clearInterval(iv);
    releaseLater(500);
  };
}
