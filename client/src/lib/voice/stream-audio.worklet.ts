// AudioWorklet: plays 48 kHz stereo f32 frames pushed from the Rust loopback
// capture through a jitter ring buffer (target ~40 ms) into a WebRTC track.
// Runs on the audio rendering thread; no allocations in process().

declare const sampleRate: number;
declare class AudioWorkletProcessor {
  readonly port: MessagePort;
}
declare function registerProcessor(name: string, ctor: unknown): void;

const CHANNELS = 2;

class StreamAudioProcessor extends AudioWorkletProcessor {
  private ring = new Float32Array(sampleRate * CHANNELS); // 1 s capacity
  private read = 0;
  private write = 0;
  private fill = 0; // in frames (samples per channel)
  private target = Math.round(sampleRate * 0.04);
  private max = Math.round(sampleRate * 0.15);
  private playing = false;
  private underruns = 0;
  private drops = 0;
  private lastLatency = 0;
  private frames = 0;
  private peak = 0;

  constructor() {
    super();
    this.port.onmessage = (e: MessageEvent<ArrayBuffer>) => this.push(e.data);
  }

  private push(buf: ArrayBuffer) {
    const ts = new Float64Array(buf, 0, 1)[0] ?? 0;
    const data = new Float32Array(buf, 8);
    const n = data.length;
    const cap = this.ring.length;
    for (let i = 0; i < n; i++) {
      const v = data[i]!;
      if (v > this.peak) this.peak = v;
      this.ring[this.write] = v;
      this.write = (this.write + 1) % cap;
    }
    this.fill += n / CHANNELS;
    if (this.fill > this.max) {
      // Drift/backlog: drop the oldest audio down to the target.
      const drop = this.fill - this.target;
      this.read = (this.read + drop * CHANNELS) % cap;
      this.fill -= drop;
      this.drops++;
    }
    this.frames++;
    if (this.frames % 100 === 0) {
      this.lastLatency = Date.now() - ts;
      this.port.postMessage({
        fillMs: (this.fill / sampleRate) * 1000,
        underruns: this.underruns,
        drops: this.drops,
        ipcMs: this.lastLatency,
        peak: this.peak,
      });
      this.peak = 0;
    }
  }

  process(_inputs: Float32Array[][], outputs: Float32Array[][]): boolean {
    const out = outputs[0];
    if (!out || out.length < 2) return true;
    const l = out[0]!;
    const r = out[1]!;
    const n = l.length;
    if (!this.playing) {
      if (this.fill >= this.target) this.playing = true;
      else {
        l.fill(0);
        r.fill(0);
        return true;
      }
    }
    if (this.fill < n) {
      this.underruns++;
      this.playing = false; // re-buffer to the target
      l.fill(0);
      r.fill(0);
      return true;
    }
    const cap = this.ring.length;
    for (let i = 0; i < n; i++) {
      l[i] = this.ring[this.read]!;
      r[i] = this.ring[this.read + 1]!;
      this.read = (this.read + 2) % cap;
    }
    this.fill -= n;
    return true;
  }
}

registerProcessor("lv-stream-audio", StreamAudioProcessor);

export {};
