import { describe, expect, it } from "vitest";
import { AUTO_MAX_DB, AUTO_MIN_DB, SpeechGate } from "./speechGate";

const FS = 48000;
const N = 128;

// Deterministic white noise.
let seed = 1;
function rnd(): number {
  seed = (seed * 1103515245 + 12345) & 0x7fffffff;
  return seed / 0x7fffffff - 0.5;
}
const amp = (db: number) => 10 ** (db / 20);

type Src = (t: number) => number;
const noise = (db: number): Src => {
  const a = amp(db) * Math.sqrt(12); // uniform → RMS 1
  return () => rnd() * a;
};
/** Voiced, speech-like tone (200 Hz + harmonics), RMS ≈ db while on. */
const voice = (db: number): Src => {
  const a = amp(db) * 0.9;
  return (t) =>
    a *
    (Math.sin(2 * Math.PI * 200 * t) +
      0.6 * Math.sin(2 * Math.PI * 400 * t) +
      0.4 * Math.sin(2 * Math.PI * 800 * t));
};
const mix =
  (...s: Src[]): Src =>
  (t) =>
    s.reduce((v, f) => v + f(t), 0);
const gated =
  (s: Src, on: (t: number) => boolean): Src =>
  (t) =>
    on(t) ? s(t) : 0;

interface Run {
  open: number; // fraction of blocks with the gate open
  speaking: number;
}

function run(g: SpeechGate, src: Src, from: number, seconds: number): Run {
  const blocks = Math.round((seconds * FS) / N);
  let open = 0;
  let speaking = 0;
  const buf = new Float32Array(N);
  for (let b = 0; b < blocks; b++) {
    for (let i = 0; i < N; i++) buf[i] = src(from + (b * N + i) / FS);
    const r = g.step(buf);
    if (r.open) open++;
    if (r.speaking) speaking++;
  }
  return { open: open / blocks, speaking: speaking / blocks };
}

describe("SpeechGate (automatic threshold)", () => {
  it("stays closed on steady room noise and settles at the lower clamp in a quiet room", () => {
    const g = new SpeechGate(FS);
    const r = run(g, noise(-75), 0, 3);
    expect(r.open).toBe(0);
    expect(g.threshold).toBe(AUTO_MIN_DB);
  });

  it("opens for speech and holds across short pauses between words", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-70), 0, 2);
    // Syllables of 180 ms with 120 ms gaps.
    const speech = mix(
      noise(-70),
      gated(voice(-28), (t) => t % 0.3 < 0.18),
    );
    const r = run(g, speech, 2, 3);
    expect(r.open).toBeGreaterThan(0.95);
    expect(r.speaking).toBeGreaterThan(0.95);
  });

  it("passes quiet speech above the threshold", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-80), 0, 2);
    const r = run(g, mix(noise(-80), voice(-42)), 2, 1);
    expect(r.open).toBeGreaterThan(0.95);
  });

  it("keeps key clicks short and does not report them as speaking", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-70), 0, 2);
    // 8 ms broadband clicks four times per second.
    const clicks = mix(
      noise(-70),
      gated(noise(-20), (t) => t % 0.25 < 0.008),
    );
    const r = run(g, clicks, 2, 2);
    expect(r.speaking).toBe(0);
    expect(r.open).toBeLessThan(0.25);
  });

  it("ignores low rumble below the speech band", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-75), 0, 2);
    const hum = (t: number) => amp(-35) * Math.SQRT2 * Math.sin(2 * Math.PI * 50 * t);
    const r = run(g, mix(noise(-75), hum), 2, 2);
    expect(r.open).toBe(0);
  });

  it("adapts to a louder room within a few seconds", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-75), 0, 3);
    // A fan switches on: room noise jumps by 30 dB.
    run(g, noise(-45), 3, 7);
    const r = run(g, noise(-45), 10, 2);
    expect(r.open).toBe(0);
    expect(g.threshold).toBeGreaterThan(-40);
    expect(g.threshold).toBeLessThanOrEqual(AUTO_MAX_DB);
    // Speech still passes in the loud room.
    const s = run(g, mix(noise(-45), voice(-22)), 12, 1);
    expect(s.open).toBeGreaterThan(0.95);
  });

  it("follows a quieter room at once", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-45), 0, 3);
    run(g, noise(-80), 3, 0.5);
    expect(g.threshold).toBe(AUTO_MIN_DB);
  });

  it("does not drift up during a long continuous sentence", () => {
    const g = new SpeechGate(FS);
    run(g, noise(-70), 0, 2);
    const speech = mix(
      noise(-70),
      gated(voice(-30), (t) => t % 0.4 < 0.32),
    );
    const r = run(g, speech, 2, 10);
    expect(r.open).toBeGreaterThan(0.95);
    expect(g.threshold).toBe(AUTO_MIN_DB);
  });
});

describe("SpeechGate (manual threshold)", () => {
  it("uses the manual value", () => {
    const g = new SpeechGate(FS);
    g.auto = false;
    g.manual = -20;
    run(g, noise(-80), 0, 1);
    expect(g.threshold).toBe(-20);
    expect(run(g, mix(noise(-80), voice(-30)), 1, 1).open).toBe(0);
    expect(run(g, mix(noise(-80), voice(-12)), 2, 1).open).toBeGreaterThan(0.95);
  });
});
