// Voice-activation decision for the mic gate worklet (pure, unit-testable).
//
// - Level is measured on a speech band (≈150 Hz–4 kHz) so rumble, hum, desk
//   thumps and high hiss count less than voice.
// - Automatic threshold = noise floor + margin, clamped to a range. The floor
//   uses minimum statistics over the last few seconds: it follows a quieter
//   room at once and a louder one within seconds (a fan switching on no
//   longer keeps the gate open for half a minute).
// - Short bursts (key clicks, taps) only open the gate briefly; the long hold
//   and the "speaking" indicator need a sustained signal like a syllable.

export const AUTO_MARGIN_DB = 15;
/** Lowest automatic threshold; quiet rooms with noise suppression end here. */
export const AUTO_MIN_DB = -50;
/** Highest automatic threshold, so speech still passes in a loud room. */
export const AUTO_MAX_DB = -30;
const HOLD_S = 0.3; // gate stays open after confirmed speech
const SHORT_HOLD_S = 0.03; // after an unconfirmed burst
const CONFIRM_S = 0.05; // sustained level that counts as speech
const FLOOR_TAU_S = 0.01; // energy smoothing for the floor estimate (short word gaps reach the floor)
const FLOOR_SUB_S = 0.5;
const FLOOR_SUBS = 6; // floor window = 3 s
const FLOOR_RISE_DB_S = 20;

class Biquad {
  private b0: number;
  private b1: number;
  private b2: number;
  private a1: number;
  private a2: number;
  private z1 = 0;
  private z2 = 0;

  // RBJ audio EQ cookbook, Q = 1/sqrt(2).
  constructor(kind: "hp" | "lp", f0: number, fs: number) {
    const w = (2 * Math.PI * f0) / fs;
    const cos = Math.cos(w);
    const alpha = Math.sin(w) / Math.SQRT2;
    const a0 = 1 + alpha;
    const b = kind === "hp" ? (1 + cos) / 2 : (1 - cos) / 2;
    this.b0 = b / a0;
    this.b1 = (kind === "hp" ? -2 * b : 2 * b) / a0;
    this.b2 = b / a0;
    this.a1 = (-2 * cos) / a0;
    this.a2 = (1 - alpha) / a0;
  }

  step(x: number): number {
    const y = this.b0 * x + this.z1;
    this.z1 = this.b1 * x - this.a1 * y + this.z2;
    this.z2 = this.b2 * x - this.a2 * y;
    return y;
  }
}

export interface GateStep {
  /** Gate should be open (voice activation). */
  open: boolean;
  /** Confirmed speech (or its hold) – drives the speaking indicator. */
  speaking: boolean;
}

export class SpeechGate {
  /** Manual threshold in dBFS, used when `auto` is off. */
  manual = -50;
  auto = true;
  /** Smoothed band level in dBFS (fast attack, slow decay) – the meter value. */
  level = -100;
  /** Noise floor estimate in dBFS. */
  floor = -70;

  private readonly hp: Biquad;
  private readonly lp: Biquad;
  private energy = -1; // smoothed band energy; < 0 = not started
  private subMin = Infinity;
  private subLeft = FLOOR_SUB_S;
  private readonly mins: number[] = [];
  private above = 0;
  private hold = 0;
  private speechHold = 0;

  constructor(private readonly fs: number) {
    this.hp = new Biquad("hp", 150, fs);
    this.lp = new Biquad("lp", 4000, fs);
  }

  get threshold(): number {
    if (!this.auto) return this.manual;
    return Math.min(AUTO_MAX_DB, Math.max(AUTO_MIN_DB, this.floor + AUTO_MARGIN_DB));
  }

  step(input: Float32Array): GateStep {
    const n = input.length;
    const blockS = n / this.fs;
    let sum = 0;
    for (let i = 0; i < n; i++) {
      const y = this.lp.step(this.hp.step(input[i]!));
      sum += y * y;
    }
    const e = sum / n;
    const db = 10 * Math.log10(e + 1e-12);
    this.level = db > this.level ? db : this.level * 0.9 + db * 0.1;
    this.trackFloor(e, blockS);

    const thr = this.threshold;
    if (this.level >= thr) {
      this.above += blockS;
      if (this.above >= CONFIRM_S) {
        this.hold = HOLD_S;
        this.speechHold = HOLD_S;
      } else {
        this.hold = Math.max(this.hold, SHORT_HOLD_S);
      }
      return { open: true, speaking: this.speechHold > 0 };
    }
    this.above = 0;
    this.hold -= blockS;
    this.speechHold -= blockS;
    return { open: this.hold > 0, speaking: this.speechHold > 0 };
  }

  private trackFloor(e: number, blockS: number): void {
    if (this.energy < 0) this.energy = e;
    else this.energy += (e - this.energy) * (1 - Math.exp(-blockS / FLOOR_TAU_S));
    const db = 10 * Math.log10(this.energy + 1e-12);
    this.subMin = Math.min(this.subMin, db);
    this.subLeft -= blockS;
    if (this.subLeft <= 0) {
      this.mins.push(this.subMin);
      if (this.mins.length > FLOOR_SUBS) this.mins.shift();
      this.subMin = Infinity;
      this.subLeft = FLOOR_SUB_S;
    }
    let target = this.subMin;
    for (const m of this.mins) target = Math.min(target, m);
    if (!Number.isFinite(target)) return;
    // Down at once, up gently so the threshold does not jump – except while
    // warming up, where the first half second sets the starting point.
    if (this.mins.length === 0 || target < this.floor) this.floor = target;
    else this.floor = Math.min(target, this.floor + FLOOR_RISE_DB_S * blockS);
  }
}
