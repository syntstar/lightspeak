// Microphone processing chain (one AudioContext, only while in a call or
// during the settings mic test):
//
//   getUserMedia (AEC/AGC/browser NS) → [RNNoise, lazy] → input gain
//     → gate worklet (voice activation / push-to-talk / mute) → track
//
// The published MediaStreamTrack comes from the destination node and stays
// the same when the device or filters change, so nothing is republished.
import gateUrl from "./mic.worklet.ts?worker&url";

export type InputMode = "vad" | "ptt" | "open";
export type NoiseMode = "off" | "standard" | "strong";

export interface MicOptions {
  deviceId: string; // "" = system default
  echoCancellation: boolean;
  autoGainControl: boolean;
  noise: NoiseMode;
  inputVolume: number; // percent, 0..200
  mode: InputMode;
  vadAuto: boolean;
  vadThreshold: number; // dBFS
}

export interface MicLevel {
  level: number;
  threshold: number;
  open: boolean;
}

let rnnoiseWasm: Promise<ArrayBuffer> | null = null;

async function rnnoiseNode(ctx: AudioContext): Promise<AudioWorkletNode> {
  const m = await import("@sapphi-red/web-noise-suppressor");
  const [{ default: worklet }, { default: url }, { default: simdUrl }] = await Promise.all([
    import("@sapphi-red/web-noise-suppressor/rnnoiseWorklet.js?url"),
    import("@sapphi-red/web-noise-suppressor/rnnoise.wasm?url"),
    import("@sapphi-red/web-noise-suppressor/rnnoise_simd.wasm?url"),
  ]);
  rnnoiseWasm ??= m.loadRnnoise({ url, simdUrl });
  await ctx.audioWorklet.addModule(worklet);
  return new m.RnnoiseWorkletNode(ctx, { maxChannels: 1, wasmBinary: await rnnoiseWasm });
}

export class MicChain {
  readonly ctx: AudioContext;
  private gate!: AudioWorkletNode;
  private gain!: GainNode;
  private dest!: MediaStreamAudioDestinationNode;
  private src: MediaStreamAudioSourceNode | null = null;
  private stream: MediaStream | null = null;
  private denoise: AudioWorkletNode | null = null;
  private opts: MicOptions;
  private closed = false;
  /** Label of the device actually in use (for hotplug decisions). */
  deviceLabel = "";
  onSpeaking: ((s: boolean) => void) | null = null;
  onLevel: ((l: MicLevel) => void) | null = null;
  /** The input device vanished; the chain already fell back to the default. */
  onDeviceLost: (() => void) | null = null;

  private constructor(opts: MicOptions) {
    this.opts = { ...opts };
    this.ctx = new AudioContext({ sampleRate: 48000, latencyHint: "interactive" });
  }

  static async create(opts: MicOptions): Promise<MicChain> {
    const m = new MicChain(opts);
    try {
      await m.ctx.audioWorklet.addModule(gateUrl);
      m.gate = new AudioWorkletNode(m.ctx, "lv-mic-gate", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
      });
      m.gain = m.ctx.createGain();
      m.dest = m.ctx.createMediaStreamDestination();
      m.dest.channelCount = 1;
      m.gain.connect(m.gate).connect(m.dest);
      m.gate.port.onmessage = (e: MessageEvent<{ speaking?: boolean } & Partial<MicLevel>>) => {
        if (typeof e.data.speaking === "boolean") m.onSpeaking?.(e.data.speaking);
        else if (typeof e.data.level === "number") m.onLevel?.(e.data as MicLevel);
      };
      m.applyGate();
      m.gain.gain.value = opts.inputVolume / 100;
      await m.acquire();
      return m;
    } catch (e) {
      await m.close();
      throw e;
    }
  }

  get track(): MediaStreamTrack {
    return this.dest.stream.getAudioTracks()[0]!;
  }

  private async acquire(): Promise<void> {
    const o = this.opts;
    const constraints: MediaTrackConstraints = {
      channelCount: 1,
      sampleRate: 48000,
      echoCancellation: o.echoCancellation,
      autoGainControl: o.autoGainControl,
      noiseSuppression: o.noise === "standard",
    };
    if (o.deviceId) constraints.deviceId = { exact: o.deviceId };
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: constraints });
    } catch (e) {
      if (!o.deviceId || (e as DOMException).name !== "OverconstrainedError") throw e;
      // Chosen device is gone: use the default and tell the caller.
      delete constraints.deviceId;
      stream = await navigator.mediaDevices.getUserMedia({ audio: constraints });
      this.onDeviceLost?.();
    }
    if (this.closed) {
      stream.getTracks().forEach((t) => t.stop());
      return;
    }
    const old = this.stream;
    this.src?.disconnect();
    this.denoise?.disconnect();
    this.stream = stream;
    const t = stream.getAudioTracks()[0]!;
    this.deviceLabel = t.label;
    t.onended = () => {
      // Unplugged: reacquire the default device.
      if (this.closed) return;
      this.opts.deviceId = "";
      this.onDeviceLost?.();
      void this.acquire().catch(() => {});
    };
    this.src = this.ctx.createMediaStreamSource(stream);
    if (o.noise === "strong") {
      try {
        this.denoise ??= await rnnoiseNode(this.ctx);
        this.src.connect(this.denoise).connect(this.gain);
      } catch {
        this.src.connect(this.gain); // fall back silently, browser NS stays off
      }
    } else {
      this.src.connect(this.gain);
    }
    old?.getTracks().forEach((x) => x.stop());
    if (this.ctx.state === "suspended") await this.ctx.resume().catch(() => {});
  }

  private applyGate(): void {
    this.gate.port.postMessage({
      mode: this.opts.mode,
      auto: this.opts.vadAuto,
      threshold: this.opts.vadThreshold,
    });
  }

  /** Applies changed settings; re-acquires the device only when needed. */
  async update(next: Partial<MicOptions>): Promise<void> {
    const prev = this.opts;
    this.opts = { ...prev, ...next };
    const o = this.opts;
    this.gain.gain.setTargetAtTime(o.inputVolume / 100, this.ctx.currentTime, 0.02);
    this.applyGate();
    if (
      o.deviceId !== prev.deviceId ||
      o.echoCancellation !== prev.echoCancellation ||
      o.autoGainControl !== prev.autoGainControl ||
      o.noise !== prev.noise
    ) {
      if (o.noise !== "strong" && this.denoise) {
        this.denoise.disconnect();
        (this.denoise as AudioWorkletNode & { destroy?: () => void }).destroy?.();
        this.denoise = null;
      }
      await this.acquire();
    }
  }

  /** Re-opens the default device (Windows default changed). */
  async reacquire(): Promise<void> {
    await this.acquire();
  }

  get usingDefault(): boolean {
    return !this.opts.deviceId;
  }

  setPtt(active: boolean): void {
    this.gate.port.postMessage({ ptt: active });
  }

  setMuted(muted: boolean): void {
    this.gate.port.postMessage({ muted });
  }

  setMeter(on: boolean): void {
    this.gate.port.postMessage({ meter: on });
  }

  /** Plays the processed signal back (settings mic test). */
  setMonitor(on: boolean, sinkId = ""): void {
    if (on) {
      this.gate.connect(this.ctx.destination);
      const c = this.ctx as AudioContext & { setSinkId?: (id: string) => Promise<void> };
      if (c.setSinkId) void c.setSinkId(sinkId).catch(() => {});
    } else {
      try {
        this.gate.disconnect(this.ctx.destination);
      } catch {
        // was not connected
      }
    }
  }

  async close(): Promise<void> {
    this.closed = true;
    this.stream?.getTracks().forEach((t) => t.stop());
    this.stream = null;
    (this.denoise as (AudioWorkletNode & { destroy?: () => void }) | null)?.destroy?.();
    await this.ctx.close().catch(() => {});
  }
}

/** Lists audio devices; labels need a prior microphone permission. */
export async function listAudioDevices(): Promise<{ inputs: MediaDeviceInfo[]; outputs: MediaDeviceInfo[] }> {
  const all = await navigator.mediaDevices.enumerateDevices();
  const real = (d: MediaDeviceInfo) => d.deviceId !== "default" && d.deviceId !== "communications";
  return {
    inputs: all.filter((d) => d.kind === "audioinput" && real(d)),
    outputs: all.filter((d) => d.kind === "audiooutput" && real(d)),
  };
}
