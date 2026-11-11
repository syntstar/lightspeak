// Voice engine: one LiveKit room at a time, end-to-end encrypted. Screen
// sharing lives in ./stream.svelte.ts and gets each room from here. Only exists while in a call; nothing runs while
// idle. Server voice states (app.voiceStates) say who is where; this engine
// adds what only the client knows: own connection, speaking, ping, quality.
import type {
  ConnectionQuality,
  DisconnectReason,
  Participant,
  RemoteParticipant,
  RemoteTrack,
  RemoteTrackPublication,
  Room,
  Track,
} from "livekit-client";
import { ApiError, errorCode } from "../api/client";
import type { Channel, ID } from "../api/types";
import { audioOf, type AudioLevel } from "../channelRules";
import { t, terr } from "../i18n/index.svelte";
import { native, inTauri, hasStore, type InputAction, type InputEvent } from "../native";
import { prefs, savePrefs } from "../prefs.svelte";
import { app, api } from "../state/app.svelte";
import { local } from "../state/local.svelte";
import { toast } from "../ui/toast.svelte";
import { log } from "../log";
import { keys } from "./e2ee.svelte";
import type { EpochKeyProvider } from "./keyProvider";
import { iceRttMs } from "./iceRtt";
import { MicChain } from "./mic";
import { play } from "./sounds";
import { SCREEN_AUDIO, StreamEngine } from "./stream.svelte";
import { AwayWatch } from "./away";

type LK = typeof import("./lk");
let lkPromise: Promise<LK> | null = null;
/** livekit-client is loaded on first use only (separate chunk). */
const loadLk = (): Promise<LK> => (lkPromise ??= import("./lk"));

export type VoiceStatus = "idle" | "connecting" | "waiting_key" | "connected" | "reconnecting";
export type Quality = "excellent" | "good" | "poor" | "lost" | "unknown";

interface JoinResp {
  livekitUrl: string;
  token: string;
  roomName: string;
  e2ee: { containerId: ID; epoch: number };
}

/** livekit protocol TrackSource.MICROPHONE (grants carry proto enum values). */
const PROTO_SOURCE_MICROPHONE = 2;
// Rejoin quickly: a dropped call should be back within seconds of the network.
const REJOIN_DELAYS = [500, 1000, 2000, 3000, 5000];
/** Track.Source.Microphone (string enum; avoids loading the SDK eagerly). */
const MIC = "microphone" as Track.Source.Microphone;
const MAX_REJOINS = 60; // ~5 min, then give up with a message
/** Errors worth retrying while reconnecting (network or SFU temporarily down). */
const RETRIABLE = new Set(["network", "unknown", "voice_unavailable", "rate_limited"]);
/** Audio watchdog period; a peer the SFU hears speaking but we decode as
 *  silence for SILENT_TICKS ticks triggers key repair, then a rejoin. */
const WATCH_MS = 2500;
const SILENT_TICKS = 3;
const HEAL_GAP_MS = 4000;
const MAX_HEALS = 2; // per call before a full rejoin

/**
 * E2EE worker per room. livekit-client does not end the worker it was given
 * when the room disconnects, so every join would leave one running.
 */
const roomWorkers = new WeakMap<object, Worker>();

function endWorker(room: object): void {
  roomWorkers.get(room)?.terminate();
  roomWorkers.delete(room);
}

/** Voice diagnostics into the local log (no keys, no content). */
function diag(event: string, data: Record<string, unknown> = {}): void {
  log("info", `voice ${event}`, data);
  // The browser has no log file: the console (F12) is its diagnostics.
  console.debug("[voice]", event, JSON.stringify(data));
}

class VoiceEngine {
  channelId = $state<ID | null>(null);
  status = $state<VoiceStatus>("idle");
  /** Last error (i18n-able code) shown in the voice panel. */
  error = $state<string | null>(null);
  ping = $state<number | null>(null);
  quality = $state<Record<ID, Quality>>({});
  speaking = $state<Record<ID, boolean>>({});
  /** No microphone available/allowed: listening only. */
  listenOnly = $state(false);
  pttActive = $state(false);
  /** Foreground app runs elevated: our PTT hook gets no input from it. */
  elevatedForeground = $state(false);
  encrypted = $state(false);
  /** The browser blocks audio playback until the next click/key press. */
  playbackBlocked = $state(false);
  /** Someone's audio does not arrive decrypted; repair in progress. */
  healing = $state(false);

  private room: Room | null = null;
  private mic: MicChain | null = null;
  private kp: EpochKeyProvider | null = null;
  private lk: LK | null = null;
  private container: ID | null = null;
  private epoch = 0;
  private gen = 0; // invalidates async work of an older join
  private epochChain: Promise<void> = Promise.resolve();
  private ownSpeaking = false;
  private trimTimer: ReturnType<typeof setTimeout> | undefined;
  private rejoinTry = 0;
  private rejoinTimer: ReturnType<typeof setTimeout> | undefined;
  private pingTimer: ReturnType<typeof setInterval> | undefined;
  private audioEls = new Map<string, HTMLAudioElement>();
  private unlistenInput: (() => void) | null = null;
  private hookOn = false;
  private watchTimer: ReturnType<typeof setInterval> | undefined;
  private peerAudio = new Map<string, { silent: number }>();
  /** Analysers on received microphone tracks (watchdog only). */
  private probes = new Map<
    string,
    { track: MediaStreamTrack; src: MediaStreamAudioSourceNode; an: AnalyserNode }
  >();
  private probeCtx: AudioContext | null = null;
  private probeBuf: Float32Array<ArrayBuffer> | null = null;
  private lastHealRejoin = 0;
  private unsubTicks = new Map<string, number>();
  private lastHeal = 0;
  private heals = 0;
  private joinedAt = 0;

  /** Screen sharing and watching (phase 5). */
  readonly stream: StreamEngine = new StreamEngine({
    get channelId() {
      return voice.channelId;
    },
    get connected() {
      return voice.status === "connected";
    },
    room: () => this.room,
    lk: () => this.lk,
    join: (id) => this.join(id),
    sendState: () => this.sendState(),
    applyVolumes: () => this.applyVolumes(),
  });

  /** Moves us into the away channel when idle. */
  private readonly away = new AwayWatch(
    {
      channelId: () => this.channelId,
      channel: (id) => app.channels[id],
      spaceChannels: (sid) => app.spaceChannels(sid),
      streaming: () => !!this.stream.own,
      osIdle: () => (inTauri ? native.idleSeconds().catch(() => null) : Promise.resolve(null)),
      move: (c) => {
        void this.join(c.id);
        toast(t("rules.movedAway", { name: c.name }), "info", 8000);
      },
    },
    !inTauri,
  );

  constructor() {
    keys.onEpochChanged((cid, epoch) => void this.onEpoch(cid, epoch));
    app.on((op, d) => {
      if (op === "voice_move") {
        const target = (d as { channelId: ID }).channelId;
        if (this.channelId && target) void this.join(target);
      } else if (op === "voice_state_update") {
        const v = d as { userId: ID; serverMuted?: boolean; listener?: boolean; noScreen?: boolean };
        if (v.userId === app.me?.id) {
          const blocked = !!v.serverMuted || !!v.listener;
          this.mic?.setMuted(local.muted || blocked);
          if (!blocked) void this.publishMic().catch(() => {});
          if (v.noScreen && this.stream.own) void this.stream.stop(true);
        }
      } else if (op === "channel_update") {
        const c = d as Channel;
        if (c.id === this.channelId && audioOf(c) !== this.micAudio) void this.republishMic();
      } else if (op === "ready" && this.channelId && this.status === "reconnecting") {
        this.scheduleRejoin(0);
      } else if (op === "session_end" && this.channelId) {
        void this.leave(false); // logged out or switched server mid-call
      }
    });
    // Nothing may run while idle: drop a hook left over from before a reload.
    if (inTauri) void native.inputStop().catch(() => {});
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", () => {
        if (!document.hidden && this.room) this.refreshSpeaking();
      });
    }
    if (typeof window !== "undefined") {
      // Network is back (Wi-Fi switch): try right away instead of waiting.
      window.addEventListener("online", () => {
        if (this.status === "reconnecting") this.scheduleRejoin(0);
      });
    }
  }

  get inCall(): boolean {
    return this.channelId !== null;
  }

  // ---------- join / leave ----------

  async join(channelId: ID, rejoin = false): Promise<void> {
    if (!rejoin && this.channelId === channelId && this.status !== "idle") return;
    const c = app.channels[channelId];
    if (!c) return;
    if (!rejoin) {
      this.rejoinTry = 0;
      await this.stream.end(); // a stream belongs to its channel
    }
    await this.teardown(false);
    const gen = ++this.gen;
    this.channelId = channelId;
    this.away.start();
    this.status = rejoin ? "reconnecting" : "connecting";
    this.error = null;
    this.listenOnly = false;
    clearTimeout(this.rejoinTimer);

    const t0 = performance.now();
    diag("join", { channelId, rejoin, attempt: this.rejoinTry });
    // Microphone, join token and key in parallel: join < 1.5 s.
    const micP = this.openMic().catch((e: unknown) => e as Error);
    const lkP = loadLk();
    try {
      const r = await api.post<JoinResp>("/voice/join", { channelId });
      if (gen !== this.gen) return;
      this.container = r.e2ee.containerId;
      this.epoch = r.e2ee.epoch;
      const keyTimer = setTimeout(() => gen === this.gen && (this.status = "waiting_key"), 600);
      const tKey = performance.now();
      const raw = await this.joinKey(r.e2ee.containerId).finally(() => clearTimeout(keyTimer));
      if (gen !== this.gen) return;
      diag("key", { epoch: this.epoch, ms: Math.round(performance.now() - tKey) });
      const lk = (this.lk = await lkP);
      const kp = new lk.EpochKeyProvider();
      await kp.setEpochKey(raw, this.epoch);
      raw.fill(0);
      this.kp = kp;
      // A rotation announced while we fetched the key: catch up right away.
      if (keys.latestEpoch(r.e2ee.containerId) > this.epoch) {
        void this.onEpoch(r.e2ee.containerId, keys.latestEpoch(r.e2ee.containerId));
      }
      const worker = lk.e2eeWorker();
      const room = new lk.Room({
        adaptiveStream: true,
        dynacast: true,
        webAudioMix: true,
        disconnectOnPageLeave: true,
        reconnectPolicy: lk.reconnectPolicy,
        stopLocalTrackOnUnpublish: false,
        encryption: { keyProvider: kp, worker },
      });
      roomWorkers.set(room, worker);
      this.room = room;
      this.wire(room);
      this.stream.wire(room, lk);
      await room.setE2EEEnabled(true);
      this.encrypted = true;
      await room.connect(r.livekitUrl, r.token, { autoSubscribe: false });
      if (gen !== this.gen) {
        await room.disconnect();
        endWorker(room);
        return;
      }
      if (prefs.audio.outputDevice) {
        await room.switchActiveDevice("audiooutput", prefs.audio.outputDevice).catch(() => false);
      }
      for (const p of room.remoteParticipants.values()) this.subscribeAudio(p);
      const mic = await micP;
      if (gen !== this.gen) return;
      if (mic instanceof Error) {
        this.listenOnly = true;
        toast(this.micErrorText(mic), "error", 6000);
      } else {
        await this.publishMic();
      }
      this.status = "connected";
      this.rejoinTry = 0;
      this.joinedAt = Date.now();
      diag("connected", {
        ms: Math.round(performance.now() - t0),
        peers: room.remoteParticipants.size,
        listenOnly: this.listenOnly,
        playback: room.canPlaybackAudio,
      });
      this.playbackBlocked = !room.canPlaybackAudio;
      if (this.playbackBlocked) this.armAudioUnlock();
      this.startWatch();
      await this.stream.connected(room);
      if (gen !== this.gen) return;
      this.sendState();
      await this.startInput();
      this.startPing();
      if (!rejoin) play("join");
      this.scheduleTrim(12_000);
    } catch (e) {
      if (gen !== this.gen) return;
      const code = e instanceof ApiError ? e.code : errorCode(e);
      diag("join_failed", { code, rejoin, ms: Math.round(performance.now() - t0) });
      if (rejoin && RETRIABLE.has(code) && this.rejoinTry < MAX_REJOINS) {
        // Still offline: stay "reconnecting" quietly and try again.
        await this.teardown(false);
        this.status = "reconnecting";
        this.scheduleRejoin(this.rejoinDelay());
        return;
      }
      await this.teardown(true);
      this.channelId = null;
      this.away.stop();
      this.status = "idle";
      this.error = code;
      toast(this.joinErrorText(code), "error", 6000);
    }
  }

  /** `notifyServer` is false after logout/server switch: the old session is
   *  gone and LiveKit's disconnect webhook updates the server instead. */
  async leave(notifyServer = true): Promise<void> {
    if (!this.channelId) return;
    ++this.gen;
    clearTimeout(this.rejoinTimer);
    await this.teardown(true);
    this.channelId = null;
    this.away.stop();
    this.status = "idle";
    this.error = null;
    if (notifyServer) void api.post("/voice/leave").catch(() => {});
    play("leave");
    this.scheduleTrim(5_000);
  }

  /** Stops everything belonging to the current room. */
  private async teardown(stopMic: boolean): Promise<void> {
    // Leaving for good: end the stream while still connected, so viewers see
    // it unpublished (closes at once) rather than us vanishing (they wait for
    // a quiet rejoin).
    if (stopMic) await this.stream.end();
    clearInterval(this.pingTimer);
    clearInterval(this.watchTimer);
    this.peerAudio.clear();
    this.unsubTicks.clear();
    this.stopProbes();
    this.healing = false;
    this.playbackBlocked = false;
    this.ping = null;
    const room = this.room;
    this.room = null;
    this.kp = null;
    this.container = null;
    this.encrypted = false;
    this.speaking = {};
    this.ownSpeaking = false;
    this.quality = {};
    for (const el of this.audioEls.values()) {
      el.srcObject = null;
      el.remove();
    }
    this.audioEls.clear();
    if (room) {
      room.removeAllListeners();
      // A quiet rejoin keeps our tracks: livekit-client would otherwise stop
      // the microphone chain's output and the stream's capture.
      await room.disconnect(stopMic).catch(() => {});
      endWorker(room);
    }
    if (!stopMic) this.stream.detach();
    if (stopMic) {
      await this.stopInput();
      await this.mic?.close();
      this.mic = null;
      this.watchDevices(false);
      this.pttActive = false;
    }
  }

  // ---------- microphone ----------

  private async openMic(): Promise<MicChain> {
    if (this.mic && this.mic.track.readyState !== "ended") return this.mic;
    if (this.mic) {
      // Its output track was stopped (it must survive rejoins): build a new chain
      // instead of publishing a dead track nobody can hear.
      diag("mic_dead");
      await this.mic.close().catch(() => {});
      this.mic = null;
    }
    const a = prefs.audio;
    const mic = await MicChain.create({
      deviceId: a.inputDevice,
      echoCancellation: a.echoCancellation,
      autoGainControl: a.autoGainControl,
      noise: a.noise,
      inputVolume: a.inputVolume,
      mode: a.mode,
      vadAuto: a.vadAuto,
      vadThreshold: a.vadThreshold,
    });
    mic.setMuted(local.muted || this.serverMuted || this.listener);
    mic.onSpeaking = (s) => {
      this.ownSpeaking = s;
      if (s) this.away.active();
      const me = app.me?.id;
      // Hidden window: no UI updates; refreshed on show.
      if (me && !document.hidden) this.speaking[me] = s;
    };
    mic.onDeviceLost = () => toast(t("voice.micLost"), "info", 5000);
    this.mic = mic;
    this.watchDevices(true);
    return mic;
  }

  /** Device-change events only while a microphone is open: a permanent
   *  listener keeps WebView2's audio service process alive while idle. */
  private watchDevices(on: boolean): void {
    const md = typeof navigator !== "undefined" ? navigator.mediaDevices : undefined;
    if (!md) return;
    md.removeEventListener("devicechange", this.onDevices);
    if (on) md.addEventListener("devicechange", this.onDevices);
  }

  private onDevices = (): void => void this.onDeviceChange();

  private async publishMic(): Promise<void> {
    const room = this.room;
    const mic = this.mic;
    if (!room || !mic || !this.mayPublishMic(room)) return;
    if (room.localParticipant.getTrackPublication(MIC)) return;
    // Audio quality follows the channel; music keeps quiet
    // passages (no DTX).
    const level = audioOf(this.channelId ? app.channels[this.channelId] : undefined);
    const presets = this.lk!.AudioPresets;
    this.micAudio = level;
    await room.localParticipant.publishTrack(mic.track, {
      source: MIC,
      dtx: level !== "music",
      red: true,
      audioPreset:
        level === "music" ? presets.musicHighQuality : level === "high" ? presets.music : presets.speech,
    });
  }

  /** Audio level the microphone was published with. */
  private micAudio: AudioLevel = "speech";

  /** The channel's audio quality changed: publish the microphone anew (the
   *  track itself keeps running, so this is a blink, not a reconnect). */
  private async republishMic(): Promise<void> {
    const room = this.room;
    const pub = room?.localParticipant.getTrackPublication(MIC);
    if (!room || !pub?.track) return;
    try {
      await room.localParticipant.unpublishTrack(pub.track, false);
      await this.publishMic();
    } catch (err) {
      log("warn", "voice: republish mic failed", err);
    }
  }

  /** LiveKit grant is the truth for server mute (it may arrive before the
   *  voice_state_update that flips `serverMuted`). */
  private mayPublishMic(room: Room): boolean {
    if (room.localParticipant.permissions?.canPublish === false) return false; // listener
    const srcs = room.localParticipant.permissions?.canPublishSources;
    if (!srcs || srcs.length === 0) return true; // empty = all sources allowed
    return (srcs as number[]).includes(PROTO_SOURCE_MICROPHONE);
  }

  /** Applies changed audio settings live (settings window). */
  async applyAudioPrefs(): Promise<void> {
    const a = prefs.audio;
    await this.mic
      ?.update({
        deviceId: a.inputDevice,
        echoCancellation: a.echoCancellation,
        autoGainControl: a.autoGainControl,
        noise: a.noise,
        inputVolume: a.inputVolume,
        mode: a.mode,
        vadAuto: a.vadAuto,
        vadThreshold: a.vadThreshold,
      })
      .catch((e: unknown) => toast(this.micErrorText(e as Error), "error"));
    if (this.room) {
      await this.room.switchActiveDevice("audiooutput", a.outputDevice || "default").catch(() => false);
      this.applyVolumes();
      await this.startInput();
    }
  }

  private micErrorText(e: Error): string {
    const n = (e as DOMException).name;
    if (n === "NotAllowedError" || n === "SecurityError") return t("voice.micDenied");
    if (n === "NotFoundError" || n === "OverconstrainedError") return t("voice.noMic");
    return t("voice.micFailed");
  }

  private joinErrorText(code: string): string {
    if (code === "e2ee_key_timeout") return t("voice.keyTimeout");
    if (code === "channel_full") return t("voice.channelFull");
    if (code === "voice_unavailable") return t("voice.unavailable");
    if (code === "network" || code === "unknown") return t("voice.connectFailed");
    return terr(code);
  }

  // ---------- mute / deafen ----------

  get serverMuted(): boolean {
    const me = app.me?.id;
    return !!(me && app.voiceStates[me]?.serverMuted);
  }

  /** Only listening here: no Speak permission, or the away channel. */
  get listener(): boolean {
    const me = app.me?.id;
    return !!(me && app.voiceStates[me]?.listener);
  }

  /** The channel does not let us share the screen. */
  get noScreen(): boolean {
    const me = app.me?.id;
    return !!(me && app.voiceStates[me]?.noScreen);
  }

  toggleMute(): void {
    const wasDeaf = local.deafened;
    local.toggleMute();
    this.afterSelfChange();
    play(wasDeaf && !local.deafened ? "undeafen" : local.muted ? "mute" : "unmute");
  }

  toggleDeafen(): void {
    local.toggleDeafen();
    this.afterSelfChange();
    play(local.deafened ? "deafen" : "undeafen");
  }

  private afterSelfChange(): void {
    this.mic?.setMuted(local.muted || this.serverMuted || this.listener);
    this.applyVolumes();
    this.sendState();
  }

  private sendState(): void {
    if (!this.channelId) return;
    app.send("voice_state_set", {
      muted: local.muted,
      deafened: local.deafened,
      streaming: !!this.stream.own,
      watching: this.stream.watchingRemote,
      stream: this.stream.own ? this.stream.info : null,
    });
  }

  // ---------- per-user volume ----------

  userVolume(id: ID): number {
    return prefs.userVolumes[id] ?? 100;
  }

  setUserVolume(id: ID, pct: number): void {
    const v = Math.max(0, Math.min(200, Math.round(pct)));
    if (v === 100) delete prefs.userVolumes[id];
    else prefs.userVolumes[id] = v;
    savePrefs();
    this.applyVolumes();
  }

  isLocallyMuted(id: ID): boolean {
    return !!prefs.localMutes[id];
  }

  toggleLocalMute(id: ID): void {
    if (prefs.localMutes[id]) delete prefs.localMutes[id];
    else prefs.localMutes[id] = true;
    savePrefs();
    this.applyVolumes();
  }

  private applyVolumes(): void {
    const room = this.room;
    if (!room) return;
    const out = prefs.audio.outputVolume / 100;
    for (const p of room.remoteParticipants.values()) {
      const v =
        local.deafened || prefs.localMutes[p.identity] ? 0 : (this.userVolume(p.identity) / 100) * out;
      p.setVolume(v, MIC);
      const sv = local.deafened ? 0 : (this.stream.streamVolume(p.identity) / 100) * out;
      p.setVolume(sv, SCREEN_AUDIO);
    }
  }

  // ---------- moderation (admins) ----------

  async moderate(userId: ID, action: "mute" | "unmute" | "disconnect" | "move", targetChannelId?: ID) {
    const ch = app.voiceStates[userId]?.channelId;
    if (!ch) return;
    await api.post(`/voice/${ch}/moderate`, { userId, action, targetChannelId });
  }

  // ---------- room events ----------

  private wire(room: Room): void {
    const { RoomEvent } = this.lk!;
    room
      .on(RoomEvent.ParticipantConnected, (p) => {
        this.subscribeAudio(p);
        play("peerJoin");
      })
      .on(RoomEvent.ParticipantDisconnected, (p) => {
        delete this.speaking[p.identity];
        delete this.quality[p.identity];
        play("peerLeave");
      })
      .on(RoomEvent.TrackPublished, (pub, p) => this.maybeSubscribe(pub, p))
      .on(RoomEvent.TrackSubscribed, (track, pub, p) => this.attach(track, pub, p))
      .on(RoomEvent.TrackUnsubscribed, (_track, pub) => this.detach(pub.trackSid))
      .on(RoomEvent.ActiveSpeakersChanged, () => this.refreshSpeaking())
      .on(RoomEvent.ConnectionQualityChanged, (q: ConnectionQuality, p: Participant) => {
        this.quality[p.identity] = q as Quality;
      })
      .on(RoomEvent.Reconnecting, () => {
        diag("reconnecting", { media: true });
        this.status = "reconnecting";
      })
      .on(RoomEvent.SignalReconnecting, () => {
        diag("reconnecting", { media: false });
        this.status = "reconnecting";
      })
      .on(RoomEvent.Reconnected, () => {
        diag("reconnected");
        this.status = "connected";
        this.sendState();
      })
      .on(RoomEvent.AudioPlaybackStatusChanged, () => {
        this.playbackBlocked = !room.canPlaybackAudio;
        diag("playback", { allowed: room.canPlaybackAudio });
        if (this.playbackBlocked) this.armAudioUnlock();
      })
      .on(RoomEvent.ParticipantPermissionsChanged, (_prev, p) => {
        if (p === room.localParticipant) void this.publishMic().catch(() => {});
      })
      .on(RoomEvent.EncryptionError, (err: Error, p?: Participant) => {
        // A peer briefly on an old key after a rotation is expected; persistent
        // failures show up as silence (never as unencrypted audio) and are
        // repaired by re-fetching the key.
        diag("encryption_error", {
          kind: (err as Error & { reason?: number }).reason,
          peer: p?.identity,
          epoch: this.epoch,
        });
        if (Date.now() - this.joinedAt > 3000) void this.heal("encryption_error");
      })
      .on(RoomEvent.Disconnected, (reason?: DisconnectReason) => void this.onDisconnected(reason));
  }

  private subscribeAudio(p: RemoteParticipant): void {
    for (const pub of p.trackPublications.values()) this.maybeSubscribe(pub, p);
  }

  private maybeSubscribe(pub: RemoteTrackPublication, _p: RemoteParticipant): void {
    // Voice only; screen video/audio are subscribed on demand (stream engine).
    if (pub.source === MIC) pub.setSubscribed(true);
  }

  private attach(track: RemoteTrack, pub: RemoteTrackPublication, _p: RemoteParticipant): void {
    if (track.kind !== "audio") return;
    const el = track.attach() as HTMLAudioElement;
    el.dataset.lv = pub.trackSid;
    this.audioEls.set(pub.trackSid, el);
    this.applyVolumes();
  }

  private detach(sid: string): void {
    const el = this.audioEls.get(sid);
    if (el) {
      el.srcObject = null;
      el.remove();
      this.audioEls.delete(sid);
    }
  }

  private async onDisconnected(reason?: DisconnectReason): Promise<void> {
    const { DisconnectReason } = this.lk!;
    diag("disconnected", { reason });
    if (!this.channelId || this.status === "idle") return;
    if (reason === DisconnectReason.CLIENT_INITIATED) return;
    if (reason === DisconnectReason.PARTICIPANT_REMOVED || reason === DisconnectReason.DUPLICATE_IDENTITY) {
      const msg =
        reason === DisconnectReason.DUPLICATE_IDENTITY ? t("voice.otherDevice") : t("voice.removed");
      await this.teardown(true);
      this.channelId = null;
      this.away.stop();
      this.status = "idle";
      play("leave");
      toast(msg, "info", 6000);
      return;
    }
    // Network loss etc.: keep the call "open" and rejoin with a fresh token.
    this.status = "reconnecting";
    this.scheduleRejoin(this.rejoinDelay());
  }

  private rejoinDelay(): number {
    return REJOIN_DELAYS[Math.min(this.rejoinTry, REJOIN_DELAYS.length - 1)]!;
  }

  private scheduleRejoin(ms: number): void {
    clearTimeout(this.rejoinTimer);
    const target = this.channelId;
    if (!target) return;
    this.rejoinTimer = setTimeout(() => {
      if (this.channelId !== target || this.status !== "reconnecting") return;
      this.rejoinTry++;
      void this.join(target, true);
    }, ms);
  }

  /** Speaking rings from LiveKit's active speakers plus our own mic gate. */
  private refreshSpeaking(): void {
    if (document.hidden) return;
    const me = app.me?.id;
    const next: Record<ID, boolean> = {};
    if (me && this.ownSpeaking) next[me] = true;
    for (const p of this.room?.activeSpeakers ?? []) if (p.identity !== me) next[p.identity] = true;
    this.speaking = next;
  }

  // ---------- e2ee rotation ----------

  /**
   * Rotations are applied strictly in order: installing a key also makes its
   * index the active send key, so an older epoch finishing its fetch after a
   * newer one must never be installed last.
   */
  /** Key for joining; follows a rotation that happens while waiting. */
  private async joinKey(cid: ID): Promise<Uint8Array> {
    for (;;) {
      try {
        return await keys.key(cid, this.epoch);
      } catch (e) {
        if (errorCode(e) !== "epoch_superseded") throw e;
        this.epoch = keys.latestEpoch(cid);
      }
    }
  }

  private onEpoch(cid: ID, epoch: number): Promise<void> {
    this.epochChain = this.epochChain.then(() => this.applyEpoch(cid, epoch));
    return this.epochChain;
  }

  private async applyEpoch(cid: ID, epoch: number): Promise<void> {
    if (cid !== this.container || epoch <= this.epoch || !this.kp) return;
    const kp = this.kp;
    try {
      const raw = await keys.key(cid, epoch);
      if (kp !== this.kp || cid !== this.container || epoch <= this.epoch) {
        raw.fill(0);
        return; // left the call or a newer epoch won meanwhile
      }
      await kp.setEpochKey(raw, epoch);
      raw.fill(0);
      this.epoch = epoch;
      if (this.error === "e2ee_error") this.error = null;
    } catch (e) {
      // superseded: the newer epoch is already queued behind us
      if (errorCode(e) !== "epoch_superseded") this.error = errorCode(e);
    }
  }

  // ---------- push-to-talk / global hotkeys ----------

  /** Re-applies hotkey bindings (after recording a key in the settings). */
  async restartInput(): Promise<void> {
    if (this.channelId) await this.startInput();
    else if (hasStore) await native.inputStop().catch(() => {});
  }

  private async startInput(): Promise<void> {
    if (!hasStore) return;
    const a = prefs.audio;
    const bindings: { action: InputAction; keys: number[] }[] = [];
    if (a.mode === "ptt" && a.pttKeys.length) bindings.push({ action: "pushToTalk", keys: a.pttKeys });
    if (a.muteKeys.length) bindings.push({ action: "toggleMute", keys: a.muteKeys });
    if (a.deafenKeys.length) bindings.push({ action: "toggleDeafen", keys: a.deafenKeys });
    this.unlistenInput ??= await native.onInput((ev) => this.onInput(ev));
    if (!bindings.length) {
      // Always stop: a hook may survive a webview reload without our flag.
      await native.inputStop().catch(() => {});
      this.hookOn = false;
      return;
    }
    try {
      await native.inputStart({ bindings, releaseDelayMs: a.pttDelay });
      this.hookOn = true;
    } catch (e) {
      console.warn("input hook", e);
      toast(t("voice.hookFailed"), "error");
    }
  }

  private async stopInput(): Promise<void> {
    if (hasStore) await native.inputStop().catch(() => {});
    this.hookOn = false;
    this.unlistenInput?.();
    this.unlistenInput = null;
    this.elevatedForeground = false;
  }

  private onInput(ev: InputEvent): void {
    if (ev.kind === "ptt") {
      this.pttActive = ev.active;
      this.mic?.setPtt(ev.active);
    } else if (ev.kind === "toggle") {
      if (ev.action === "toggleMute") this.toggleMute();
      else if (ev.action === "toggleDeafen") this.toggleDeafen();
    } else if (ev.kind === "elevatedForeground") {
      this.elevatedForeground = ev.elevated;
    }
  }

  // ---------- memory ----------

  /**
   * One working-set trim after the call settled: WebRTC loads the
   * hardware video encoder into the GPU process on the first peer connection
   * (~150 MB, also for audio-only calls); untouched afterwards, so trimming
   * returns it to Windows. After leaving, call leftovers go the same way.
   */
  private scheduleTrim(ms: number): void {
    if (!inTauri) return;
    clearTimeout(this.trimTimer);
    this.trimTimer = setTimeout(() => void native.memoryTrim().catch(() => {}), ms);
  }

  // ---------- devices, ping ----------

  private async onDeviceChange(): Promise<void> {
    if (!this.mic) return;
    const all = await navigator.mediaDevices.enumerateDevices().catch(() => [] as MediaDeviceInfo[]);
    const a = prefs.audio;
    if (a.outputDevice && !all.some((d) => d.kind === "audiooutput" && d.deviceId === a.outputDevice)) {
      await this.room?.switchActiveDevice("audiooutput", "default").catch(() => false);
      toast(t("voice.outputLost"), "info", 5000);
    }
    if (this.mic.usingDefault) {
      // Windows default input changed (e.g. headset plugged in): follow it.
      const def = all.find((d) => d.kind === "audioinput" && d.deviceId === "default");
      if (def && !def.label.includes(this.mic.deviceLabel)) await this.mic.reacquire().catch(() => {});
    }
  }

  // ---------- audio watchdog / self-healing ----------

  /**
   * Every WATCH_MS: a peer whose microphone the SFU reports as speaking (the
   * audio level travels unencrypted in the RTP header) but whose decoded
   * audio energy does not grow cannot be heard here – wrong/stale key, stuck
   * subscription or dead decoder. Repair: resubscribe, re-fetch the key,
   * finally rejoin. Costs one getStats per peer every few seconds in a call.
   */
  private startWatch(): void {
    clearInterval(this.watchTimer);
    this.peerAudio.clear();
    this.unsubTicks.clear();
    this.watchTimer = setInterval(() => void this.watch(), WATCH_MS);
  }

  private async watch(): Promise<void> {
    const room = this.room;
    if (!room || this.status !== "connected") return;
    let silentPeer = false;
    let anyHeard = false;
    for (const p of room.remoteParticipants.values()) {
      const pub = p.getTrackPublication(MIC);
      if (!pub || pub.isMuted) {
        this.peerAudio.delete(p.identity);
        continue;
      }
      if (!pub.isSubscribed || !pub.track) {
        // A fresh subscription takes a moment; only a stuck one is retried.
        const n = (this.unsubTicks.get(p.identity) ?? 0) + 1;
        this.unsubTicks.set(p.identity, n);
        if (n >= 2) {
          diag("resubscribe", { peer: p.identity });
          this.unsubTicks.delete(p.identity);
          pub.setSubscribed(false);
          pub.setSubscribed(true);
        }
        continue;
      }
      this.unsubTicks.delete(p.identity);
      // Decoded audio, measured on the received track itself. (getStats'
      // totalAudioEnergy stays 0 with webAudioMix: Chrome only computes it
      // in its own playout path, which we bypass.)
      const peak = this.probe(p.identity, pub.track.mediaStreamTrack);
      if (peak === null) continue;
      const prev = this.peerAudio.get(p.identity)?.silent ?? 0;
      const heard = peak > 1e-3;
      if (heard) anyHeard = true;
      // Only digital silence while the SFU measures speech counts.
      let silent = heard ? 0 : p.isSpeaking && p.audioLevel > 0.02 && peak < 1e-4 ? prev + 1 : prev;
      if (silent >= SILENT_TICKS) {
        diag("silent_peer", { peer: p.identity, level: Math.round(p.audioLevel * 100) / 100 });
        silent = 0;
        silentPeer = true;
      }
      this.peerAudio.set(p.identity, { silent });
    }
    for (const id of this.probes.keys()) if (!room.remoteParticipants.has(id)) this.dropProbe(id);
    if (silentPeer) await this.heal("silent_peer");
    else if (anyHeard && this.healing) {
      diag("healed");
      this.healing = false;
      this.heals = 0;
      if (this.error === "e2ee_error") this.error = null;
    }
  }

  /** Peak of the last ~0.7 s decoded from a peer's microphone; null = cannot measure now. */
  private probe(id: string, mt: MediaStreamTrack): number | null {
    // Reuse the microphone's context; listen-only calls get a small own one.
    let ctx: AudioContext | null = this.mic?.ctx ?? null;
    if (!ctx || ctx.state === "closed") ctx = this.probeCtx ??= new AudioContext({ latencyHint: "playback" });
    if (ctx.state !== "running") return null; // autoplay-blocked: no data, no verdict
    const pr = this.probes.get(id);
    if (!pr || pr.track !== mt || pr.src.context !== ctx) {
      this.dropProbe(id);
      const src = ctx.createMediaStreamSource(new MediaStream([mt]));
      const an = ctx.createAnalyser();
      an.fftSize = 32768;
      src.connect(an);
      this.probes.set(id, { track: mt, src, an });
      return null; // fills up until the next tick
    }
    const buf = (this.probeBuf ??= new Float32Array(32768));
    pr.an.getFloatTimeDomainData(buf);
    let peak = 0;
    for (let i = 0; i < buf.length; i++) {
      const v = Math.abs(buf[i]!);
      if (v > peak) peak = v;
    }
    return peak;
  }

  private dropProbe(id: string): void {
    const pr = this.probes.get(id);
    if (!pr) return;
    pr.src.disconnect();
    this.probes.delete(id);
  }

  private stopProbes(): void {
    for (const id of [...this.probes.keys()]) this.dropProbe(id);
    void this.probeCtx?.close().catch(() => {});
    this.probeCtx = null;
    this.probeBuf = null;
  }

  /** Re-fetches the media key (or a newer epoch); after MAX_HEALS a rejoin. */
  private async heal(why: string): Promise<void> {
    const cid = this.container;
    const kp = this.kp;
    const ch = this.channelId;
    if (!cid || !kp || !ch || Date.now() - this.lastHeal < HEAL_GAP_MS) return;
    this.lastHeal = Date.now();
    this.healing = true;
    this.error = "e2ee_error";
    if (++this.heals > MAX_HEALS && Date.now() - this.lastHealRejoin > 120_000) {
      this.lastHealRejoin = Date.now();
      diag("heal_rejoin", { why });
      this.heals = 0;
      this.status = "reconnecting";
      void this.join(ch, true);
      return;
    }
    try {
      const raw = await keys.refresh(cid, this.epoch);
      if (kp !== this.kp) return;
      const latest = keys.latestEpoch(cid);
      if (latest > this.epoch) {
        raw.fill(0);
        diag("heal_epoch", { why, from: this.epoch, to: latest });
        await this.onEpoch(cid, latest);
      } else {
        await kp.setEpochKey(raw, this.epoch);
        raw.fill(0);
        diag("heal_key", { why, epoch: this.epoch });
      }
    } catch (e) {
      diag("heal_failed", { why, code: errorCode(e) });
    }
  }

  /** Browsers start audio only after a user gesture: unlock on the next one. */
  private armAudioUnlock(): void {
    const unlock = () => {
      window.removeEventListener("pointerdown", unlock, true);
      window.removeEventListener("keydown", unlock, true);
      void this.resumeAudio();
    };
    window.addEventListener("pointerdown", unlock, true);
    window.addEventListener("keydown", unlock, true);
  }

  async resumeAudio(): Promise<void> {
    const room = this.room;
    if (!room) return;
    await room.startAudio().catch(() => {});
    this.playbackBlocked = !room.canPlaybackAudio;
    diag("playback_resume", { allowed: room.canPlaybackAudio });
  }

  private startPing(): void {
    clearInterval(this.pingTimer);
    // Media path RTT (iceRtt.ts); the signalling RTT only until ICE has one.
    const read = async () => {
      const room = this.room;
      const pcs = room?.engine?.pcManager;
      let rtt: number | null = null;
      for (const t of [pcs?.publisher, pcs?.subscriber]) {
        const report = await t?.getStats()?.catch(() => undefined);
        if (report) rtt = iceRttMs(report);
        if (rtt !== null) break;
      }
      if (room !== this.room) return;
      const sig = room?.engine?.client?.rtt;
      if (rtt !== null) this.ping = rtt;
      else if (typeof sig === "number" && sig > 0) this.ping = Math.round(sig);
    };
    void read();
    this.pingTimer = setInterval(() => {
      if (document.visibilityState === "visible") void read();
    }, 5000);
  }
}

export const voice = new VoiceEngine();

// Dev builds only: lets debugging tools reach the live instance (Vite HMR
// would otherwise hand a second module instance to ad-hoc imports).
if (import.meta.env.DEV) Object.assign(globalThis, { __lvVoice: voice });
