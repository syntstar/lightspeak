// Screen sharing: publishing our screen or a window as H.264
// with its sound, and watching others on demand. Video is only subscribed
// while someone actually watches; nothing here runs
// outside a call. Owned by the voice engine, which hands over each room.
import type {
  LocalTrackPublication,
  LocalVideoTrack,
  RemoteParticipant,
  RemoteTrack,
  RemoteTrackPublication,
  Room,
  Track,
} from "livekit-client";
import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { getCurrentWindow } from "@tauri-apps/api/window";
import type { ID, StreamInfo } from "../api/types";
import { t } from "../i18n/index.svelte";
import { inTauri, isWeb } from "../native";
import { prefs, savePrefs } from "../prefs.svelte";
import { app } from "../state/app.svelte";
import { toast } from "../ui/toast.svelte";
import { play } from "./sounds";
import { withStartBitrate } from "./startBitrate";
import { PreviewGate, RampTracker } from "./streamRamp";
import { startStreamAudio, type StreamAudioHandle, type StreamAudioSource } from "./streamAudio";
import {
  PREVIEW_LAYER,
  contentMode,
  degradation,
  maxBitrate,
  previewEncoding,
  startKbps,
  videoConstraints,
  type StreamPrefs,
} from "./streamPresets";

type LK = typeof import("./lk");

/** Track.Source values (string enums; avoids loading the SDK eagerly). */
export const SCREEN = "screen_share" as Track.Source.ScreenShare;
export const SCREEN_AUDIO = "screen_share_audio" as Track.Source.ScreenShareAudio;

/** How often the own stream's encoder state is checked for viewers. */
const QUALITY_MS = 1500;

/** A streamer that vanished (connection loss, quiet rejoin) gets this long to come back. */
const RETURN_MS = 15_000;
/**
 * …but only while the server still lists it as streaming in our channel: a
 * rejoin restores that within a second, leaving for good does not (seconds).
 */
const GONE_TICKS = 3;
/** Unpublishing on leave must not hold up leaving. */
const UNPUBLISH_MS = 1500;

/** No sound for this long from a captured window → offer the source picker. */
const SILENT_MS = 6000;
const LOUD = 0.002;

/** Publisher transports whose answers already carry our start bitrate hint. */
const hinted = new WeakSet<object>();

/** What the stream engine needs from the voice engine. */
export interface StreamHost {
  readonly channelId: ID | null;
  readonly connected: boolean;
  room(): Room | null;
  lk(): LK | null;
  join(channelId: ID): Promise<void>;
  sendState(): void;
  applyVolumes(): void;
}

export type StreamAudioKind = "app" | "system" | "pick";

export interface OwnStream {
  surface: "monitor" | "window";
  /** getDisplayMedia track label, e.g. `window:<hwnd>:0` or `screen:<n>:0`. */
  label: string;
  /** Where the sound comes from; null = no sound. */
  audio: { kind: StreamAudioKind; processName: string | null } | null;
  /** Captured window stayed silent: maybe its sound comes from another process. */
  silent: boolean;
  /** Settings the stream was started (or last updated) with. */
  cfg: StreamPrefs;
}

/** A captured window or screen, with what the dialog shows about it. */
export interface CaptureSource {
  track: MediaStreamTrack;
  surface: "monitor" | "window";
  label: string;
  /** Window caption and app (e.g. `chrome`); null for screens or until known. */
  title: string | null;
  app: string | null;
  /** Browser client: sound shared in the browser's picker (tab or system audio). */
  audioTrack?: MediaStreamTrack | null;
}

/**
 * Browser client: the browser's picker delivers the sound together
 * with the picture, so a handle just wraps that track.
 */
function browserAudio(track: MediaStreamTrack): StreamAudioHandle {
  return {
    track,
    processName: null,
    // The source owns the track (released with the picture), so turning the
    // sound off and on again keeps working without a new picker.
    stop: async () => {},
    dispose: async () => {},
  };
}

export class StreamEngine {
  /** Our own stream, null while not streaming. */
  own = $state<OwnStream | null>(null);
  /** Source picked in the dialog but not live yet (shown as preview). */
  pending = $state.raw<CaptureSource | null>(null);
  /** Source of the running stream. */
  current = $state.raw<CaptureSource | null>(null);
  busy = $state(false);
  /** Streams we watch (user ids, may include ourselves for the preview). */
  watching = $state<ID[]>([]);
  /** Video track per watched user once it arrived. */
  tiles = $state.raw<Record<ID, Track>>({});
  /** Stream view replaces the content area. */
  view = $state(false);
  /** Stream shown large; null = grid of all watched streams. */
  focus = $state<ID | null>(null);
  /** Theater: stream fills the whole window (sidebars hidden). */
  theater = $state(false);
  fullscreen = $state(false);
  /** Pre-dialog open. */
  dialog = $state(false);

  private videoTrack: MediaStreamTrack | null = null;
  private videoPub: LocalTrackPublication | null = null;
  private audio: StreamAudioHandle | null = null;
  private audioPub: LocalTrackPublication | null = null;
  private lastLoud = 0;
  private silenceTimer: ReturnType<typeof setInterval> | undefined;
  private probeTimer: ReturnType<typeof setTimeout> | undefined;
  private unlistenEnded: UnlistenFn | null = null;
  private readonly onTrackEnded = () => void this.stop();
  /** Browser client: the sound track of the live source (owned like the video). */
  private sourceAudio: MediaStreamTrack | null = null;
  private readonly onPendingEnded = () => this.cancelPending();
  /** What viewers learn about our stream (server voice state). */
  info = $state.raw<StreamInfo | null>(null);
  private qualityTimer: ReturnType<typeof setInterval> | undefined;
  private ramp = new RampTracker(0);
  private gate = new PreviewGate();
  private previewOff = false;
  /** Watched streamers that vanished, waiting for their return (by user). */
  private returns = new Map<ID, ReturnType<typeof setInterval>>();
  /** Start bitrate hint (kbit/s) for answers while our stream is published. */
  private startHint: number | null = null;
  private starts = 0;

  constructor(private host: StreamHost) {}

  // ---------- own stream ----------

  /** Users watching our stream (from their server voice states). */
  get viewers(): ID[] {
    const me = app.me?.id;
    const ch = this.host.channelId;
    if (!me || !ch || !this.own) return [];
    return Object.values(app.voiceStates)
      .filter((s) => s.channelId === ch && s.userId !== me && s.watching?.includes(me))
      .map((s) => s.userId);
  }

  /** Remote streamers we watch (what the server gets to see). */
  get watchingRemote(): ID[] {
    const me = app.me?.id;
    return this.watching.filter((id) => id !== me);
  }

  /**
   * "Share screen": opens the dialog and – in the same click, which the
   * picker needs as user activation – the window/screen picker.
   */
  share(): void {
    this.dialog = true;
    if (!this.own && !this.pending) void this.pick();
  }

  /**
   * Opens the window/screen picker. Not streaming: the source becomes the
   * dialog's preview until "Go live". Streaming: the stream switches to it
   * right away – viewers stay, the sound follows (7.7).
   */
  async pick(): Promise<boolean> {
    if (this.busy || !this.host.connected) return false;
    this.busy = true;
    try {
      const cap = await this.capture(this.own?.cfg ?? prefs.stream);
      if (!cap) return false;
      const track = cap.video;
      const src: CaptureSource = {
        track,
        surface: this.surfaceOf(track),
        label: track.label,
        title: null,
        app: null,
        audioTrack: cap.audio,
      };
      // The picker opens on this tab next time.
      prefs.stream = { ...prefs.stream, surface: src.surface };
      savePrefs();
      const room = this.host.room();
      if (this.own) {
        if (!room || !this.videoPub?.track) {
          track.stop();
          cap.audio?.stop();
          return false;
        }
        await this.switchTo(room, src, this.own.cfg);
      } else {
        this.cancelPending();
        track.addEventListener("ended", this.onPendingEnded);
        this.pending = src;
      }
      void this.describe(src);
      return true;
    } catch (e) {
      console.warn("screen share", e);
      toast(t("stream.failed"), "error");
      return false;
    } finally {
      this.busy = false;
    }
  }

  /** Publishes the previewed source with the given settings. */
  async goLive(p: StreamPrefs): Promise<boolean> {
    const room = this.host.room();
    const src = this.pending;
    if (!src || !room || !this.host.connected || this.busy || this.own) return false;
    // Taken synchronously: closing the dialog right after must not discard it.
    this.pending = null;
    src.track.removeEventListener("ended", this.onPendingEnded);
    this.busy = true;
    try {
      prefs.stream = { ...p, surface: src.surface };
      savePrefs();
      await src.track.applyConstraints(videoConstraints(p)).catch(() => {});
      src.track.contentHint = contentMode(p);
      await this.publish(room, src, p);
      return true;
    } catch (e) {
      console.warn("screen share", e);
      toast(t("stream.failed"), "error");
      if (!this.videoPub) await this.stop(true);
      return false;
    } finally {
      this.busy = false;
    }
  }

  /** Dialog dismissed without going live: release the previewed source. */
  cancelPending(): void {
    const src = this.pending;
    if (!src) return;
    this.pending = null;
    src.track.removeEventListener("ended", this.onPendingEnded);
    src.track.stop();
    src.audioTrack?.stop();
  }

  /** Picks a source and goes live in one go (scripts and tests). */
  async start(p: StreamPrefs = prefs.stream): Promise<boolean> {
    if (this.own) return this.pick();
    if (!this.pending && !(await this.pick())) return false;
    return this.goLive(p);
  }

  /** Looks up the window caption and app for the dialog. */
  private async describe(src: CaptureSource): Promise<void> {
    if (src.surface !== "window" || !inTauri) return;
    const info = await invoke<{ title: string; processName: string | null } | null>("capture_window_info", {
      label: src.label,
    }).catch(() => null);
    if (!info) return;
    const named: CaptureSource = {
      ...src,
      title: info.title.trim() || null,
      app: info.processName?.replace(/\.exe$/i, "") ?? null,
    };
    if (this.pending?.track === src.track) this.pending = named;
    if (this.current?.track === src.track) this.current = named;
  }

  /** Stops our stream. */
  async stop(quiet = false): Promise<void> {
    if (!this.own && !this.videoTrack) return;
    const room = this.host.room();
    this.own = null;
    this.current = null;
    await this.stopAudio(room);
    const pub = this.videoPub;
    this.videoPub = null;
    this.startHint = null;
    this.stopQuality();
    if (pub?.track && room) await room.localParticipant.unpublishTrack(pub.track, true).catch(() => {});
    this.releaseVideo();
    this.dropWatch(app.me?.id);
    this.host.sendState();
    if (!quiet) play("streamStop");
  }

  /** Applies changed settings to the running stream without a new picker. */
  async update(p: StreamPrefs): Promise<void> {
    const room = this.host.room();
    const own = this.own;
    if (!room || !own) return;
    prefs.stream = { ...p };
    savePrefs();
    await this.applyVideo(p);
    if (p.audio && !this.audio && this.canAudio()) await this.startAudio(room, this.defaultAudioSource(own));
    else if (!p.audio && this.audio) await this.stopAudio(room);
  }

  /** Stream sound possible: native capture, or the browser's picker shared some. */
  canAudio(src: CaptureSource | null = this.current): boolean {
    if (inTauri) return true;
    return isWeb && !!src?.audioTrack && src.audioTrack.readyState === "live";
  }

  private async applyVideo(p: StreamPrefs): Promise<void> {
    const pub = this.videoPub;
    if (!this.own || !pub?.track || !this.videoTrack) return;
    await this.videoTrack.applyConstraints(videoConstraints(p)).catch(() => {});
    this.videoTrack.contentHint = contentMode(p);
    const vt = pub.track as LocalVideoTrack;
    await vt.setDegradationPreference(degradation(p)).catch(() => {});
    const sender = vt.sender;
    if (sender) {
      const params = sender.getParameters();
      const { width = 0, height = 0 } = this.videoTrack.getSettings();
      for (const enc of params.encodings ?? []) {
        if (enc.rid === "q") {
          // Preview layer: follows a new source size/fps, keeps its own rate.
          Object.assign(enc, previewEncoding(width, height, p.fps));
          continue;
        }
        enc.maxBitrate = maxBitrate(p);
        enc.maxFramerate = p.fps;
      }
      await sender.setParameters(params).catch((e: unknown) => console.warn("encoding", e));
    }
    this.own.cfg = { ...p };
    this.ramp.restart(performance.now());
    void this.checkQuality();
  }

  /**
   * Manual sound source ("Audio from: …") for when the window → process
   * mapping missed: a process id, the whole system, or back to the default.
   */
  async pickAudio(choice: number | "default" | "system"): Promise<void> {
    const room = this.host.room();
    const own = this.own;
    if (!room || !own || !inTauri) return;
    const src: StreamAudioSource =
      choice === "default"
        ? this.defaultAudioSource(own)
        : choice === "system"
          ? { kind: "screen" }
          : { kind: "pid", pid: choice };
    await this.startAudio(room, src);
  }

  private async capture(
    p: StreamPrefs,
  ): Promise<{ video: MediaStreamTrack; audio: MediaStreamTrack | null } | null> {
    try {
      const s = await navigator.mediaDevices.getDisplayMedia({
        video: { ...videoConstraints(p), displaySurface: p.surface },
        // Desktop: sound comes from WASAPI. Browser: the picker
        // offers "share tab audio" / "share system audio" – music
        // quality, and without the call's own voices where supported.
        audio: isWeb
          ? {
              echoCancellation: false,
              noiseSuppression: false,
              autoGainControl: false,
              channelCount: 2,
              restrictOwnAudio: true,
            }
          : false,
        ...(isWeb ? { systemAudio: "include", windowAudio: "system" } : {}),
        selfBrowserSurface: "exclude",
        surfaceSwitching: "exclude",
      } as DisplayMediaStreamOptions);
      const tr = s.getVideoTracks()[0] ?? null;
      const audio = s.getAudioTracks()[0] ?? null;
      if (!tr) {
        audio?.stop();
        return null;
      }
      tr.contentHint = contentMode(p);
      return { video: tr, audio };
    } catch (e) {
      const n = (e as DOMException).name;
      // NotAllowedError/AbortError: picker cancelled, not an error for the user.
      if (n !== "NotAllowedError" && n !== "AbortError") toast(t("stream.captureFailed"), "error");
      return null;
    }
  }

  private surfaceOf(track: MediaStreamTrack): "monitor" | "window" {
    // Browsers report the surface directly; a shared tab counts as a window.
    const ds = (track.getSettings() as { displaySurface?: string }).displaySurface;
    if (ds) return ds === "monitor" ? "monitor" : "window";
    return track.label.startsWith("window:") ? "window" : "monitor";
  }

  private defaultAudioSource(own: Pick<OwnStream, "surface" | "label">): StreamAudioSource {
    return own.surface === "window" ? { kind: "window", label: own.label } : { kind: "screen" };
  }

  private async publish(room: Room, src: CaptureSource, p: StreamPrefs): Promise<void> {
    const track = src.track;
    this.videoTrack = track;
    this.sourceAudio = src.audioTrack ?? null;
    this.current = src;
    track.addEventListener("ended", this.onTrackEnded);
    const own: OwnStream = {
      surface: src.surface,
      label: src.label,
      audio: null,
      silent: false,
      cfg: { ...p },
    };
    this.own = own;
    await this.publishVideo(room, p);
    if (p.audio && this.canAudio(src)) await this.startAudio(room, this.defaultAudioSource(own));
    this.host.sendState();
    play("streamStart");
  }

  private async publishVideo(room: Room, p: StreamPrefs): Promise<void> {
    const lk = this.host.lk();
    if (!this.videoTrack || !lk) return;
    // libwebrtc applies a start value only when it changed: alternate by one
    // kbit/s so every new stream starts high, not just the first of a call.
    this.startHint = startKbps(p) - (this.starts++ % 2);
    this.hintAnswers(room);
    this.videoPub = await room.localParticipant.publishTrack(this.videoTrack, {
      source: SCREEN,
      name: "screen",
      // Automatic = H.264 (hardware encoders everywhere); VP8 only on request.
      videoCodec: prefs.streamCodec === "vp8" ? "vp8" : "h264",
      backupCodec: false,
      // Full layer + a 360p preview for small tiles; dynacast pauses
      // whichever layer nobody watches.
      simulcast: true,
      screenShareSimulcastLayers: [
        new lk.VideoPreset(
          PREVIEW_LAYER.width,
          PREVIEW_LAYER.height,
          PREVIEW_LAYER.maxBitrate,
          PREVIEW_LAYER.fps,
        ),
      ],
      screenShareEncoding: { maxBitrate: maxBitrate(p), maxFramerate: p.fps, priority: "high" },
      degradationPreference: degradation(p),
    });
    if (this.watching.includes(app.me?.id ?? "") && this.videoPub.track)
      this.setTile(app.me!.id, this.videoPub.track);
    this.probeEncoder();
    this.watchQuality();
  }

  /**
   * Tells viewers whether full quality arrives yet: after a start or a
   * settings change the full layer needs a few seconds until the bandwidth
   * estimate allows its bitrate (rules: streamRamp.ts).
   */
  private watchQuality(): void {
    clearInterval(this.qualityTimer);
    this.ramp = new RampTracker(performance.now());
    this.gate = new PreviewGate();
    this.previewOff = false;
    this.qualityTimer = setInterval(() => void this.checkQuality(), QUALITY_MS);
    void this.checkQuality();
  }

  private stopQuality(): void {
    clearInterval(this.qualityTimer);
    this.qualityTimer = undefined;
    this.info = null;
  }

  private async checkQuality(): Promise<void> {
    const own = this.own;
    const sender = (this.videoPub?.track as LocalVideoTrack | undefined)?.sender;
    if (!own || !sender || !this.videoTrack) return;
    let report: RTCStatsReport;
    try {
      report = await sender.getStats();
    } catch {
      return;
    }
    if (this.own !== own) return;
    let full: Record<string, unknown> | null = null;
    report.forEach((s: Record<string, unknown>) => {
      if (s.type === "outbound-rtp" && s.kind === "video" && s.rid !== "q") full = s;
    });
    const f = full as Record<string, unknown> | null;
    const ramp = this.ramp.update(
      f && {
        active: f.active !== false && typeof f.frameHeight === "number",
        targetBps: typeof f.targetBitrate === "number" ? f.targetBitrate : 0,
        bytesSent: typeof f.bytesSent === "number" ? f.bytesSent : undefined,
        bandwidthLimited: f.qualityLimitationReason === "bandwidth",
      },
      maxBitrate(own.cfg),
      performance.now(),
    );
    await this.gatePreview(sender, f);
    if (this.own !== own) return;
    const was = this.info;
    const height =
      this.videoTrack.getSettings().height ?? (typeof f?.frameHeight === "number" ? f.frameHeight : 0);
    const next: StreamInfo = { height: Math.round(height), fps: own.cfg.fps, ramp };
    if (next.height < 1) return;
    if (was && was.height === next.height && was.fps === next.fps && was.ramp === next.ramp) return;
    this.info = next;
    this.host.sendState();
  }

  /** Lets the full layer take over from the preview on a slow uplink (streamRamp.ts). */
  private async gatePreview(sender: RTCRtpSender, f: Record<string, unknown> | null): Promise<void> {
    const params = sender.getParameters();
    const preview = params.encodings?.find((e) => e.rid === "q");
    const full = params.encodings?.find((e) => e.rid !== "q");
    if (!preview || !full) return;
    const target = typeof f?.targetBitrate === "number" ? f.targetBitrate : 0;
    const wasOff = this.previewOff;
    this.previewOff = this.gate.update(
      {
        fullWanted: full.active !== false,
        previewActive: preview.active !== false,
        fullStarved: !!f && f.active !== false && f.qualityLimitationReason === "bandwidth" && !target,
        fullTargetBps: target,
      },
      performance.now(),
    );
    // Dynacast may switch the preview back on, so this is enforced on every check.
    if (this.previewOff ? preview.active === false : !wasOff) return;
    preview.active = !this.previewOff;
    await sender.setParameters(params).catch((e: unknown) => console.warn("preview layer", e));
  }

  /**
   * Adds the start bitrate hint to the SFU's answers on this room's publisher
   * transport (see startBitrate.ts). Without a running stream the answers stay
   * untouched, and libwebrtc keeps its estimate.
   */
  private hintAnswers(room: Room): void {
    const pub = room.engine?.pcManager?.publisher;
    if (!pub || hinted.has(pub)) return;
    hinted.add(pub);
    const orig = pub.setRemoteDescription.bind(pub);
    pub.setRemoteDescription = (sd: RTCSessionDescriptionInit, offerId: number) => {
      const kbps = this.startHint;
      if (kbps && sd.type === "answer" && sd.sdp) sd = { type: sd.type, sdp: withStartBitrate(sd.sdp, kbps) };
      return orig(sd, offerId);
    };
  }

  /** Remembers which encoder WebRTC picked (Settings → Streaming shows it). */
  private probeEncoder(): void {
    clearTimeout(this.probeTimer);
    this.probeTimer = setTimeout(() => {
      const sender = this.videoPub?.track?.sender;
      if (!sender) return;
      void sender
        .getStats()
        .then((report) => {
          let impl = "";
          let hw: boolean | null = null;
          let codecId = "";
          report.forEach((s: Record<string, unknown>) => {
            if (s.type !== "outbound-rtp" || s.kind !== "video") return;
            if (typeof s.encoderImplementation === "string") impl = s.encoderImplementation;
            if (typeof s.powerEfficientEncoder === "boolean") hw = s.powerEfficientEncoder;
            if (typeof s.codecId === "string") codecId = s.codecId;
          });
          if (!impl) return;
          const mime = (report.get(codecId) as { mimeType?: string } | undefined)?.mimeType ?? "";
          prefs.streamEncoder = { codec: mime.replace(/^video\//, ""), impl, hw, at: Date.now() };
          savePrefs();
        })
        .catch(() => {});
    }, 5000);
  }

  private async switchTo(room: Room, src: CaptureSource, p: StreamPrefs): Promise<void> {
    const own = this.own!;
    const track = src.track;
    const old = this.videoTrack;
    const oldAudio = this.sourceAudio;
    await this.videoPub!.track!.replaceTrack(track, { userProvidedTrack: true });
    old?.removeEventListener("ended", this.onTrackEnded);
    old?.stop();
    this.videoTrack = track;
    this.sourceAudio = src.audioTrack ?? null;
    track.addEventListener("ended", this.onTrackEnded);
    own.surface = src.surface;
    own.label = src.label;
    own.silent = false;
    this.current = src;
    await this.applyVideo(p);
    // The sound follows the new source.
    if (p.audio && this.canAudio(src)) await this.startAudio(room, this.defaultAudioSource(own));
    else if (this.audio) await this.stopAudio(room);
    if (oldAudio && oldAudio !== this.sourceAudio) oldAudio.stop();
  }

  private releaseVideo(): void {
    this.videoTrack?.removeEventListener("ended", this.onTrackEnded);
    this.videoTrack?.stop();
    this.videoTrack = null;
    this.sourceAudio?.stop();
    this.sourceAudio = null;
  }

  // ---------- stream audio ----------

  private async startAudio(room: Room, src: StreamAudioSource): Promise<void> {
    const lk = this.host.lk();
    if (!lk || !this.own) return;
    const old = this.audio;
    let h: StreamAudioHandle;
    try {
      if (!inTauri) {
        const track = this.current?.audioTrack;
        if (!track || track.readyState !== "live") throw new Error("no_audio");
        h = browserAudio(track);
      } else h = await startStreamAudio(src);
    } catch (e) {
      // The native side already stopped the previous capture.
      console.warn("stream audio", e);
      await this.stopAudio(room);
      toast(t(String(e) === "own_window" ? "stream.audioOwnWindow" : "stream.audioFailed"), "error");
      return;
    }
    if (!this.own || this.host.room() !== room) {
      await h.stop();
      return;
    }
    this.audio = h;
    try {
      if (this.audioPub?.track) {
        await this.audioPub.track.replaceTrack(h.track, { userProvidedTrack: true });
      } else {
        this.audioPub = await room.localParticipant.publishTrack(h.track, {
          source: SCREEN_AUDIO,
          name: "screen-audio",
          audioPreset: lk.AudioPresets.musicHighQualityStereo,
          dtx: false,
          red: false,
          forceStereo: true,
        });
      }
    } finally {
      await old?.dispose();
    }
    const kind: StreamAudioKind = !inTauri
      ? "system"
      : src.kind === "pid"
        ? "pick"
        : src.kind === "window"
          ? "app"
          : "system";
    this.own.audio = { kind, processName: h.processName };
    this.own.silent = false;
    this.watchSilence(h, inTauri && src.kind === "window");
    if (inTauri)
      this.unlistenEnded ??= await listen<string | null>(
        "stream-audio-ended",
        () => void this.onAudioEnded(),
      );
    else h.track.addEventListener("ended", () => void this.onAudioEnded(), { once: true });
  }

  private async stopAudio(room: Room | null): Promise<void> {
    clearInterval(this.silenceTimer);
    this.unlistenEnded?.();
    this.unlistenEnded = null;
    const pub = this.audioPub;
    this.audioPub = null;
    if (pub?.track && room) await room.localParticipant.unpublishTrack(pub.track, false).catch(() => {});
    const h = this.audio;
    this.audio = null;
    await h?.stop();
    if (this.own) {
      this.own.audio = null;
      this.own.silent = false;
    }
  }

  /** The captured app closed or the device went away. */
  private async onAudioEnded(): Promise<void> {
    if (!this.audio) return;
    await this.stopAudio(this.host.room());
    if (this.own) toast(t("stream.audioEnded"), "info");
  }

  private watchSilence(h: StreamAudioHandle, enabled: boolean): void {
    clearInterval(this.silenceTimer);
    this.lastLoud = Date.now();
    h.onStats = (s) => {
      if (s.peak <= LOUD) return;
      this.lastLoud = Date.now();
      if (this.own?.silent) this.own.silent = false;
    };
    if (!enabled) return;
    // Stats only flow while the app renders sound, so check on a timer.
    this.silenceTimer = setInterval(() => {
      const own = this.own;
      if (own && !own.silent && Date.now() - this.lastLoud > SILENT_MS) own.silent = true;
    }, 2000);
  }

  // ---------- watching ----------

  isWatching(id: ID): boolean {
    return this.watching.includes(id);
  }

  /** Watches a stream: joins its voice channel if needed, subscribes video+sound. */
  async watch(userId: ID): Promise<void> {
    const me = app.me?.id;
    if (userId === me) {
      if (!this.own) return;
      this.addWatch(userId);
      if (this.videoPub?.track) this.setTile(userId, this.videoPub.track);
      return;
    }
    const st = app.voiceStates[userId];
    if (!st?.channelId || !st.streaming) return;
    if (this.host.channelId !== st.channelId) {
      await this.host.join(st.channelId);
      if (this.host.channelId !== st.channelId || !this.host.connected) return;
    }
    this.addWatch(userId);
    this.subscribe(userId, true);
    this.host.sendState();
  }

  unwatch(userId: ID): void {
    this.cancelReturn(userId);
    if (!this.watching.includes(userId)) return;
    if (userId !== app.me?.id) this.subscribe(userId, false);
    this.dropWatch(userId);
    if (userId !== app.me?.id) this.host.sendState();
  }

  /** Shows the stream view again (after switching to a text channel). */
  open(focus?: ID): void {
    if (!this.watching.length) return;
    this.view = true;
    if (focus) this.focus = focus;
  }

  close(): void {
    this.view = false;
    void this.setTheater(false);
  }

  streamVolume(id: ID): number {
    return prefs.streamVolumes[id] ?? 100;
  }

  setStreamVolume(id: ID, pct: number): void {
    const v = Math.max(0, Math.min(200, Math.round(pct)));
    if (v === 100) delete prefs.streamVolumes[id];
    else prefs.streamVolumes[id] = v;
    savePrefs();
    this.host.applyVolumes();
  }

  async setTheater(on: boolean): Promise<void> {
    this.theater = on;
    if (!on && this.fullscreen) await this.setFullscreen(false);
  }

  /** Real window fullscreen (F11 / double click), always together with theater. */
  async setFullscreen(on: boolean): Promise<void> {
    this.fullscreen = on;
    if (on) this.theater = true;
    if (inTauri)
      await getCurrentWindow()
        .setFullscreen(on)
        .catch(() => {});
    else if (isWeb && typeof document !== "undefined") {
      if (on && !document.fullscreenElement)
        await document.documentElement.requestFullscreen().catch(() => {});
      else if (!on && document.fullscreenElement) await document.exitFullscreen().catch(() => {});
    }
  }

  private addWatch(id: ID): void {
    if (!this.watching.includes(id)) this.watching = [...this.watching, id];
    this.view = true;
    this.focus = id;
  }

  private dropWatch(id: ID | undefined): void {
    if (!id || !this.watching.includes(id)) return;
    this.watching = this.watching.filter((x) => x !== id);
    const { [id]: _gone, ...rest } = this.tiles;
    this.tiles = rest;
    if (this.focus === id) this.focus = this.watching.length === 1 ? this.watching[0]! : null;
    if (!this.watching.length) this.close();
  }

  private setTile(id: ID, track: Track): void {
    this.tiles = { ...this.tiles, [id]: track };
  }

  private subscribe(userId: ID, on: boolean): void {
    const p = this.host.room()?.remoteParticipants.get(userId);
    if (!p) return;
    for (const pub of p.trackPublications.values()) {
      if (pub.source === SCREEN || pub.source === SCREEN_AUDIO) pub.setSubscribed(on);
    }
  }

  // ---------- room lifecycle (called by the voice engine) ----------

  /** New room created (before connect): wire the stream events. */
  wire(room: Room, lk: LK): void {
    const { RoomEvent } = lk;
    room
      .on(RoomEvent.TrackPublished, (pub: RemoteTrackPublication, p: RemoteParticipant) => {
        if ((pub.source === SCREEN || pub.source === SCREEN_AUDIO) && this.watching.includes(p.identity))
          pub.setSubscribed(true);
      })
      .on(
        RoomEvent.TrackSubscribed,
        (track: RemoteTrack, pub: RemoteTrackPublication, p: RemoteParticipant) => {
          if (pub.source !== SCREEN || track.kind !== "video") return;
          this.setTile(p.identity, track);
          if (this.cancelReturn(p.identity)) this.host.sendState(); // the server dropped us as viewer meanwhile
        },
      )
      .on(
        RoomEvent.TrackUnsubscribed,
        (_t: RemoteTrack, pub: RemoteTrackPublication, p: RemoteParticipant) => {
          if (pub.source === SCREEN && this.tiles[p.identity]) {
            const { [p.identity]: _gone, ...rest } = this.tiles;
            this.tiles = rest;
          }
        },
      )
      .on(RoomEvent.TrackUnpublished, (pub: RemoteTrackPublication, p: RemoteParticipant) => {
        if (pub.source !== SCREEN) return;
        // Still in the room: the stream ended, leave its tile. Gone (LiveKit
        // removes the participant before unpublishing its tracks): a
        // connection loss or quiet rejoin, wait for the stream to return.
        if (room.remoteParticipants.has(p.identity)) this.unwatch(p.identity);
        else this.awaitReturn(p.identity);
      })
      .on(RoomEvent.ParticipantDisconnected, (p: RemoteParticipant) => this.awaitReturn(p.identity))
      // A streamer back from a rejoin may bring its stream along; LiveKit only
      // reports tracks published after both sides joined.
      .on(RoomEvent.ParticipantConnected, (p: RemoteParticipant) => {
        if (this.watching.includes(p.identity)) this.subscribe(p.identity, true);
      });
  }

  /**
   * Keeps a vanished stream's place ("loading") until it returns, the
   * streamer is gone for GONE_TICKS seconds, or RETURN_MS pass.
   */
  private awaitReturn(id: ID): void {
    if (!this.watching.includes(id) || this.returns.has(id)) return;
    const { [id]: _gone, ...rest } = this.tiles;
    this.tiles = rest;
    const until = Date.now() + RETURN_MS;
    let gone = 0;
    const tick = setInterval(() => {
      if (this.tiles[id]) {
        this.cancelReturn(id);
        return;
      }
      const st = app.voiceStates[id];
      gone = st?.channelId === this.host.channelId && st.streaming ? 0 : gone + 1;
      if (gone >= GONE_TICKS || Date.now() > until) this.unwatch(id);
    }, 1000);
    this.returns.set(id, tick);
  }

  private cancelReturn(id: ID): boolean {
    const t = this.returns.get(id);
    if (t === undefined) return false;
    clearInterval(t);
    this.returns.delete(id);
    return true;
  }

  /** Room connected (first join or quiet rejoin): restore stream + watching. */
  async connected(room: Room): Promise<void> {
    for (const id of this.watchingRemote) this.subscribe(id, true);
    if (this.own && this.videoTrack && !this.videoPub) {
      try {
        await this.publishVideo(room, this.own.cfg);
        if (this.audio) {
          const lk = this.host.lk()!;
          this.audioPub = await room.localParticipant.publishTrack(this.audio.track, {
            source: SCREEN_AUDIO,
            name: "screen-audio",
            audioPreset: lk.AudioPresets.musicHighQualityStereo,
            dtx: false,
            red: false,
            forceStereo: true,
          });
        }
      } catch (e) {
        console.warn("republish stream", e);
        await this.stop(true);
        toast(t("stream.failed"), "error");
      }
    }
  }

  /** Room torn down for a quiet rejoin: keep our tracks and what we watch. */
  detach(): void {
    clearInterval(this.qualityTimer);
    this.videoPub = null;
    this.audioPub = null;
    this.tiles = {};
  }

  /** Call ended or channel switched: stop everything. */
  async end(): Promise<void> {
    const was = !!this.own;
    // Still connected (leave, channel switch): unpublish first, so viewers
    // close the stream at once instead of waiting for a rejoin.
    const room = this.host.room();
    const tracks = [this.videoPub?.track, this.audioPub?.track].filter((t) => !!t);
    if (room && tracks.length) {
      const unpublish = Promise.all(
        tracks.map((t) => room.localParticipant.unpublishTrack(t, false).catch(() => undefined)),
      );
      await Promise.race([unpublish, new Promise((r) => setTimeout(r, UNPUBLISH_MS))]);
    }
    for (const t of this.returns.values()) clearInterval(t);
    this.returns.clear();
    this.own = null;
    this.current = null;
    this.cancelPending();
    this.dialog = false;
    clearInterval(this.silenceTimer);
    this.unlistenEnded?.();
    this.unlistenEnded = null;
    const h = this.audio;
    this.audio = null;
    this.audioPub = null;
    this.videoPub = null;
    this.startHint = null;
    this.stopQuality();
    await h?.stop();
    this.releaseVideo();
    this.watching = [];
    this.tiles = {};
    this.focus = null;
    this.close();
    if (was) play("streamStop");
  }
}
