// Screen share quality presets. Pure functions, no SDK.

export type StreamSurface = "monitor" | "window";
export type StreamResolution = "720" | "1080" | "1440" | "source";
export type StreamFps = 15 | 30 | 60;
export type StreamContent = "auto" | "motion" | "detail";

export interface StreamPrefs {
  surface: StreamSurface;
  resolution: StreamResolution;
  fps: StreamFps;
  content: StreamContent;
  audio: boolean;
}

export const DEFAULT_STREAM: StreamPrefs = {
  surface: "window",
  resolution: "1080",
  fps: 30,
  content: "auto",
  audio: true,
};

export const RESOLUTIONS: readonly StreamResolution[] = ["720", "1080", "1440", "source"];
export const FPS: readonly StreamFps[] = [15, 30, 60];
export const CONTENTS: readonly StreamContent[] = ["auto", "motion", "detail"];

const SIZE: Record<Exclude<StreamResolution, "source">, [number, number]> = {
  "720": [1280, 720],
  "1080": [1920, 1080],
  "1440": [2560, 1440],
};

/** Max. bitrate in bit/s. 30 and 60 fps follow the table; 15 fps ≈ 60 % of 30. */
const BITRATE: Record<StreamResolution, Record<StreamFps, number>> = {
  "720": { 15: 1_500_000, 30: 2_500_000, 60: 4_000_000 },
  "1080": { 15: 3_000_000, 30: 5_000_000, 60: 8_000_000 },
  "1440": { 15: 5_000_000, 30: 8_000_000, 60: 12_000_000 },
  source: { 15: 6_000_000, 30: 10_000_000, 60: 15_000_000 },
};

export function maxBitrate(p: Pick<StreamPrefs, "resolution" | "fps">): number {
  return BITRATE[p.resolution][p.fps];
}

/**
 * Small simulcast layer ("q") for stream tiles in the grid/strip:
 * 360p, at most 15 fps, 400 kbit/s (LiveKit's h360fps15). Dynacast pauses it
 * while no viewer shows a small tile, so it only costs when it saves.
 */
export const PREVIEW_LAYER = { width: 640, height: 360, maxBitrate: 400_000, fps: 15 } as const;

/** Encoding parameters of the preview layer for a source of w×h at the chosen fps. */
export function previewEncoding(
  w: number,
  h: number,
  fps: StreamFps,
): { scaleResolutionDownBy: number; maxBitrate: number; maxFramerate: number } {
  const short = Math.min(w, h);
  return {
    scaleResolutionDownBy: short > 0 ? Math.max(1, short / PREVIEW_LAYER.height) : 1,
    maxBitrate: PREVIEW_LAYER.maxBitrate,
    maxFramerate: Math.min(PREVIEW_LAYER.fps, fps),
  };
}

/** Upper bound of the start bitrate hint (kbit/s): bounds the first second on slow uplinks. */
export const START_KBPS_MAX = 5000;

/**
 * Start bitrate hint for a new stream: 90 % of what both layers may
 * use, as livekit-client intends, capped at START_KBPS_MAX.
 */
export function startKbps(p: Pick<StreamPrefs, "resolution" | "fps">): number {
  return Math.min(START_KBPS_MAX, Math.round(((maxBitrate(p) + PREVIEW_LAYER.maxBitrate) * 0.9) / 1000));
}

/** "Automatic" content: 60 fps means games/video, otherwise windows/text. */
export function contentMode(p: Pick<StreamPrefs, "content" | "fps">): "motion" | "detail" {
  if (p.content !== "auto") return p.content;
  return p.fps >= 60 ? "motion" : "detail";
}

export function degradation(p: Pick<StreamPrefs, "content" | "fps">): RTCDegradationPreference {
  return contentMode(p) === "motion" ? "maintain-framerate" : "maintain-resolution";
}

/** Track constraints: downscale to the preset (never upscale), cap the rate. */
export function videoConstraints(p: Pick<StreamPrefs, "resolution" | "fps">): MediaTrackConstraints {
  const c: MediaTrackConstraints = { frameRate: { ideal: p.fps, max: p.fps } };
  if (p.resolution !== "source") {
    const [w, h] = SIZE[p.resolution];
    c.width = { max: w };
    c.height = { max: h };
  }
  return c;
}

/** "8 Mbit/s" style label for the estimated upload. */
export function bitrateLabel(bps: number, locale: string): string {
  const n = bps / 1_000_000;
  return `${n.toLocaleString(locale, { maximumFractionDigits: 1 })} Mbit/s`;
}

export function sanitizeStream(raw: unknown): StreamPrefs {
  const s = { ...DEFAULT_STREAM };
  if (!raw || typeof raw !== "object") return s;
  const r = raw as Record<string, unknown>;
  if (r.surface === "monitor" || r.surface === "window") s.surface = r.surface;
  if ((RESOLUTIONS as readonly unknown[]).includes(r.resolution))
    s.resolution = r.resolution as StreamResolution;
  if ((FPS as readonly unknown[]).includes(r.fps)) s.fps = r.fps as StreamFps;
  if ((CONTENTS as readonly unknown[]).includes(r.content)) s.content = r.content as StreamContent;
  if (typeof r.audio === "boolean") s.audio = r.audio;
  return s;
}
