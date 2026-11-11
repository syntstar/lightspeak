// AudioWorklet: microphone gate for voice activation and push-to-talk plus
// level metering. Runs on the audio thread; posts a message only when the
// "speaking" state flips, and level updates (~20/s) only while metering is
// requested (settings mic test, own speaking indicator is the flag above).
import { SpeechGate } from "./speechGate";

declare const sampleRate: number;
declare class AudioWorkletProcessor {
  readonly port: MessagePort;
}
declare function registerProcessor(name: string, ctor: unknown): void;

type Mode = "vad" | "ptt" | "open";

interface Config {
  mode?: Mode;
  /** Manual threshold in dBFS (-100..0). */
  threshold?: number;
  /** Automatic threshold (noise floor + margin). */
  auto?: boolean;
  ptt?: boolean;
  muted?: boolean;
  meter?: boolean;
}

const ATTACK_S = 0.004;
const RELEASE_S = 0.06;

class MicGate extends AudioWorkletProcessor {
  private mode: Mode = "vad";
  private ptt = false;
  private muted = false;
  private meter = false;
  private readonly vad = new SpeechGate(sampleRate);

  private gain = 0;
  private open = false;
  private speaking = false;
  private blocks = 0;

  constructor() {
    super();
    this.port.onmessage = (e: MessageEvent<Config>) => {
      const c = e.data;
      if (c.mode) this.mode = c.mode;
      if (typeof c.threshold === "number") this.vad.manual = c.threshold;
      if (typeof c.auto === "boolean") this.vad.auto = c.auto;
      if (typeof c.ptt === "boolean") this.ptt = c.ptt;
      if (typeof c.muted === "boolean") this.muted = c.muted;
      if (typeof c.meter === "boolean") this.meter = c.meter;
    };
  }

  process(inputs: Float32Array[][], outputs: Float32Array[][]): boolean {
    const input = inputs[0]?.[0];
    const out = outputs[0]?.[0];
    if (!out) return true;
    const n = out.length;
    if (!input) {
      out.fill(0);
      return true;
    }

    // Always analysed, so the floor is ready when switching to voice activation.
    const v = this.vad.step(input);
    let want: boolean;
    if (this.muted) want = false;
    else if (this.mode === "ptt") want = this.ptt;
    else if (this.mode === "open") want = true;
    else want = v.open;
    this.open = want;

    const target = want ? 1 : 0;
    const step = n / sampleRate / (want ? ATTACK_S : RELEASE_S) / n;
    for (let i = 0; i < n; i++) {
      if (this.gain < target) this.gain = Math.min(target, this.gain + step);
      else if (this.gain > target) this.gain = Math.max(target, this.gain - step);
      out[i] = input[i]! * this.gain;
    }

    // "Speaking" = gate open and actual speech (not a click or just the hold).
    const speakingNow = want && (this.mode === "vad" ? v.speaking : this.vad.level > -60);
    if (speakingNow !== this.speaking) {
      this.speaking = speakingNow;
      this.port.postMessage({ speaking: speakingNow });
    }
    if (this.meter && ++this.blocks >= 19) {
      this.blocks = 0;
      this.port.postMessage({
        level: Math.max(-100, this.vad.level),
        threshold: this.vad.threshold,
        open: this.open,
      });
    }
    return true;
  }
}

registerProcessor("lv-mic-gate", MicGate);

export {};
