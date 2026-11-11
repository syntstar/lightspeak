// Native stream audio (WASAPI process loopback) → AudioWorklet → MediaStreamTrack.
import { Channel, invoke } from "@tauri-apps/api/core";
import workletUrl from "./stream-audio.worklet.ts?worker&url";

export type StreamAudioSource =
  | { kind: "window"; label: string }
  | { kind: "screen" }
  /** Manual pick: a process that currently plays audio. */
  | { kind: "pid"; pid: number };

export interface AudioSession {
  pid: number;
  processName: string | null;
  peak: number;
}

/** Processes currently playing audio (never our own). */
export function audioSessions(): Promise<AudioSession[]> {
  return invoke("audio_sessions");
}

export interface StreamAudioStats {
  fillMs: number;
  underruns: number;
  drops: number;
  ipcMs: number;
  /** Highest sample value in the last second (0..1). */
  peak: number;
}

export interface StreamAudioHandle {
  track: MediaStreamTrack;
  processName: string | null;
  /** Stops the native capture and releases the track. */
  stop(): Promise<void>;
  /** Releases only the JS side (a newer capture already replaced the native one). */
  dispose(): Promise<void>;
  onStats?: (s: StreamAudioStats) => void;
}

export async function startStreamAudio(source: StreamAudioSource): Promise<StreamAudioHandle> {
  const ctx = new AudioContext({ sampleRate: 48000, latencyHint: "interactive" });
  await ctx.audioWorklet.addModule(workletUrl);
  const node = new AudioWorkletNode(ctx, "lv-stream-audio", {
    numberOfInputs: 0,
    numberOfOutputs: 1,
    outputChannelCount: [2],
  });
  const dest = ctx.createMediaStreamDestination();
  dest.channelCount = 2;
  node.connect(dest);

  const channel = new Channel<ArrayBuffer>();
  channel.onmessage = (buf) => {
    if (buf instanceof ArrayBuffer) node.port.postMessage(buf, [buf]);
  };
  let info: { pid: number; processName: string | null };
  try {
    info = await invoke("stream_audio_start", { source, channel });
  } catch (e) {
    await ctx.close();
    throw e;
  }
  const track = dest.stream.getAudioTracks()[0]!;
  const handle: StreamAudioHandle = {
    track,
    processName: info.processName,
    async stop() {
      await invoke("stream_audio_stop").catch(() => {});
      await handle.dispose();
    },
    async dispose() {
      channel.onmessage = () => {};
      track.stop();
      node.disconnect();
      await ctx.close().catch(() => {});
    },
  };
  node.port.onmessage = (e: MessageEvent<StreamAudioStats>) => handle.onStats?.(e.data);
  return handle;
}
