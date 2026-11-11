// Central client state (Svelte 5 runes). Filled from the gateway `ready`
// snapshot and kept current by gateway events (handled idempotently).
import { Api, PROTOCOL_VERSION, errorCode } from "../api/client";
import { Gateway, type GatewayStatus } from "../api/gateway";
import type {
  Channel,
  ID,
  Me,
  Member,
  Presence,
  ReadState,
  Ready,
  Relationship,
  Space,
  SpaceRole,
  Status,
  User,
  VoiceState,
} from "../api/types";
import { native, inTauri, isWeb, hasStore, type AppInfo, type StoredSession } from "../native";
import { fetchServerInfo } from "../server";
import { log } from "../log";
import { prefs, savePrefs, serverPrefs } from "../prefs.svelte";
import { idGt } from "./ids";
import { toast } from "../ui/toast.svelte";
import { setErrorParams, t } from "../i18n/index.svelte";
import { DEFAULT_LIMITS, limitParams, readLimits } from "../limits";
import {
  effectiveLevel,
  isMuted,
  sanitizeNotifyMap,
  type NotifyLevel,
  type NotifyMap,
  type TargetSetting,
} from "../notifyRules";

export type Phase = "boot" | "auth" | "app";
export const HOME = "@me";

export const api = new Api();

/** Deleted accounts keep their messages under a neutral name. */
export const displayUser = (u: User): User =>
  u.deleted ? { ...u, displayName: t("user.deleted"), username: "" } : u;

class AppState {
  phase = $state<Phase>("boot");
  info = $state<AppInfo>({
    profile: "default",
    version: "0.0.0",
    startMinimized: false,
    dev: true,
    computerName: "",
    startedAt: 0,
    updater: false,
  });
  conn = $state<GatewayStatus>("connecting");
  private readyLogged = false;
  retryInMs = $state(0);
  updateRequired = $state(false);
  limits = $state(DEFAULT_LIMITS);
  authNotice = $state<string | null>(null);
  /** Invite link waiting to be handled (deep link or pasted). */
  pendingInvite = $state<string | null>(null);
  /** Origin of the selected server ("" = none; mirrors api.base). */
  server = $state("");
  /** Auth screen shows the server picker instead of the login form. */
  pickServer = $state(false);

  me = $state<Me | null>(null);
  deviceId = $state<ID | null>(null);
  spaces = $state<Record<ID, Space>>({});
  spaceOrder = $state<ID[]>([]);
  channels = $state<Record<ID, Channel>>({});
  users = $state<Record<ID, User>>({});
  readStates = $state<Record<ID, ReadState>>({});
  presences = $state<Record<ID, Presence>>({});
  voiceStates = $state<Record<ID, VoiceState>>({});
  relationships = $state<Record<ID, Relationship>>({});
  members = $state<Record<ID, Member[]>>({});
  /** Synced per-user settings (GET/PUT /users/@me/settings). */
  synced: Record<string, unknown> = {};
  notify = $state<NotifyMap>({});
  private syncTimer: ReturnType<typeof setTimeout> | undefined;
  membersLoading = $state<Record<ID, boolean>>({});

  /** Currently shown space (or HOME) and channel. */
  spaceId = $state<ID>(HOME);
  channelId = $state<ID | null>(null);

  quickSwitcher = $state(false);
  settingsOpen = $state(false);
  /** Right-hand search panel (replaces the member list while open). */
  searchOpen = $state(false);
  searchQuery = $state("");
  /** Bumped to run the search panel's query immediately. */
  searchNonce = $state(0);
  /** Message to scroll to and highlight once the channel shows it. */
  jumpTarget = $state<{ channelId: ID; messageId: ID } | null>(null);

  private gw: Gateway | null = null;
  private session: StoredSession | null = null;

  // ---------- derived helpers ----------

  get space(): Space | undefined {
    return this.spaces[this.spaceId];
  }

  get channel(): Channel | undefined {
    return this.channelId ? this.channels[this.channelId] : undefined;
  }

  spaceChannels(spaceId: ID): Channel[] {
    return Object.values(this.channels)
      .filter((c) => c.spaceId === spaceId)
      .sort((a, b) => a.position - b.position || (a.id < b.id ? -1 : 1));
  }

  dms(): Channel[] {
    return Object.values(this.channels)
      .filter((c) => c.kind === "dm" || c.kind === "group_dm")
      .sort((a, b) => (idGt(a.lastMessageId ?? a.id, b.lastMessageId ?? b.id) ? -1 : 1));
  }

  /** Unread and not muted (muted channels never show as unread). */
  isUnread(c: Channel): boolean {
    if (c.kind !== "text" && c.kind !== "dm" && c.kind !== "group_dm") return false;
    if (c.id === this.channelId && this.focused) return false;
    if (isMuted(this.notify, c)) return false;
    return idGt(c.lastMessageId, this.readStates[c.id]?.lastReadId ?? "0");
  }

  /** Mention badge count; suppressed when notifications are set to "none". */
  mentions(c: Channel): number {
    if (effectiveLevel(this.notify, c) === "none") return 0;
    return this.readStates[c.id]?.mentionCount ?? 0;
  }

  notifyLevel(c: Channel): NotifyLevel {
    return effectiveLevel(this.notify, c);
  }

  muted(c: Channel): boolean {
    return isMuted(this.notify, c);
  }

  /** Changes the notification setting of a channel/space/DM (synced).
   *  `level: null` removes the override (inherit from the space/default). */
  setNotify(targetId: ID, patch: { level?: NotifyLevel | null; muted?: boolean }): void {
    const cur: TargetSetting = { ...this.notify[targetId] };
    if (patch.level === null) delete cur.level;
    else if (patch.level) cur.level = patch.level;
    if (patch.muted === true) cur.muted = true;
    else if (patch.muted === false) delete cur.muted;
    if (cur.level || cur.muted) this.notify[targetId] = cur;
    else delete this.notify[targetId];
    this.saveSynced();
  }

  private saveSynced(): void {
    clearTimeout(this.syncTimer);
    this.syncTimer = setTimeout(() => {
      this.synced = { ...this.synced, notify: $state.snapshot(this.notify) };
      void api
        .put("/users/@me/settings", this.synced)
        .catch((e: unknown) => console.warn("settings", errorCode(e)));
    }, 600);
  }

  private applySynced(s: unknown): void {
    this.synced = s && typeof s === "object" ? (s as Record<string, unknown>) : {};
    this.notify = sanitizeNotifyMap(this.synced.notify);
  }

  spaceUnread(spaceId: ID): { unread: boolean; mentions: number } {
    let unread = false;
    let mentions = 0;
    for (const c of Object.values(this.channels)) {
      const inScope = spaceId === HOME ? c.kind === "dm" || c.kind === "group_dm" : c.spaceId === spaceId;
      if (!inScope) continue;
      if (this.isUnread(c)) unread = true;
      mentions += this.mentions(c);
    }
    return { unread, mentions };
  }

  get anyUnread(): boolean {
    for (const c of Object.values(this.channels)) {
      if (this.mentions(c) > 0) return true;
      if ((c.kind === "dm" || c.kind === "group_dm") && this.isUnread(c)) return true;
    }
    return false;
  }

  status(userId: ID): Status {
    if (userId === this.me?.id)
      return this.me.status === "invisible" ? "invisible" : (this.me.status ?? "online");
    return this.presences[userId]?.status ?? "offline";
  }

  user(id: ID): User | undefined {
    if (id === this.me?.id) return this.me;
    const u = this.users[id];
    return u && displayUser(u);
  }

  channelTitle(c: Channel): string {
    if (c.kind === "dm" || c.kind === "group_dm") {
      if (c.name) return c.name;
      const others = (c.memberIds ?? []).filter((id) => id !== this.me?.id);
      return others.map((id) => this.user(id)?.displayName ?? "…").join(", ") || "…";
    }
    return c.name;
  }

  relation(userId: ID): Relationship["kind"] | undefined {
    return this.relationships[userId]?.kind;
  }

  /** The other user of a 1:1 DM. */
  dmPeer(c: Channel): User | undefined {
    if (c.kind !== "dm") return undefined;
    const other = (c.memberIds ?? []).find((id) => id !== this.me?.id);
    return other ? this.user(other) : undefined;
  }

  /** Users this client knows about (members of loaded spaces, DMs, friends). */
  knownUsers(): User[] {
    return Object.values(this.users).filter((u) => u.id !== this.me?.id);
  }

  voiceMembers(channelId: ID): VoiceState[] {
    return Object.values(this.voiceStates).filter((v) => v.channelId === channelId);
  }

  // ---------- window focus (for unread) ----------
  focused = $state(typeof document === "undefined" ? true : document.hasFocus());

  // ---------- lifecycle ----------

  async boot(): Promise<void> {
    if (hasStore) {
      this.info = await native.appInfo();
      this.pendingInvite = await native.takePendingInvite();
    }
    api.onUnauthorized = () => void this.logout(true);
    if (isWeb) {
      // The browser client belongs to the server that serves it.
      const origin = location.origin;
      void fetchServerInfo(origin)
        .then((info) => {
          serverPrefs(origin).name = info.name;
          savePrefs();
        })
        .catch(() => {});
      if (await this.resume(origin)) return;
      this.pickServer = false;
      this.phase = "auth";
      return;
    }
    if (prefs.server && (await this.resume(prefs.server))) return;
    if (prefs.server) this.useServer(prefs.server);
    this.pickServer = !prefs.server;
    this.phase = "auth";
  }

  /** Makes `origin` the active server (no network access). */
  useServer(origin: string, name?: string): void {
    api.base = origin;
    this.server = origin;
    prefs.server = origin;
    const sp = serverPrefs(origin);
    if (name) sp.name = name;
    savePrefs();
  }

  /**
   * Switches to `origin` and continues its saved session if there is one.
   * Returns false when the user has to log in there.
   */
  async resume(origin: string): Promise<boolean> {
    const s = hasStore ? await native.sessionGet(origin).catch(() => null) : null;
    this.useServer(origin);
    if (!s) return false;
    if (isWeb && !(await native.deviceKey(origin, false).catch(() => null))) {
      // Site data partly cleared (key gone, session left): start over cleanly.
      await api.post("/auth/logout").catch(() => {});
      await native.sessionClear(origin).catch(() => {});
      return false;
    }
    this.startSession(s);
    return true;
  }

  /**
   * Leaves the current server without logging out (its session stays saved,
   * so switching back needs no password) and shows the server picker or,
   * with `to`, continues on that server.
   */
  async switchServer(to?: string): Promise<void> {
    this.gw?.stop();
    this.gw = null;
    api.token = null;
    api.cookieSession = false;
    this.session = null;
    this.reset();
    this.authNotice = null;
    if (to && (await this.resume(to))) return;
    this.pickServer = !to;
    this.phase = "auth";
  }

  startSession(s: StoredSession): void {
    this.session = s;
    api.token = s.token || null;
    api.cookieSession = isWeb;
    this.phase = "app";
    this.conn = "connecting";
    const wsUrl = api.base.replace(/^http/, "ws") + "/ws";
    let settled = false;
    this.gw = new Gateway(wsUrl, s.token, PROTOCOL_VERSION, {
      onEvent: (op, d) => this.onEvent(op, d),
      onStatus: (st, retry) => {
        this.conn = st;
        this.retryInMs = retry ?? 0;
        if (st === "ready" && !settled && inTauri) {
          // Loading the main view leaves transient pages resident; the timed
          // trim after process start misses a login that comes later.
          settled = true;
          setTimeout(() => void native.memoryTrim().catch(() => {}), 15_000);
        }
        if (st === "ready" && !this.readyLogged && this.info.startedAt) {
          // Cold start budget: process start → usable.
          this.readyLogged = true;
          log("info", `ready ${Date.now() - this.info.startedAt} ms after start`);
        }
      },
      onAuthLost: () => void this.logout(true),
    });
    this.gw.start();
  }

  async completeAuth(token: string, me: Me, deviceId: ID, publicKey: string): Promise<void> {
    const s: StoredSession = { server: api.base, token, userId: me.id, deviceId };
    // Remembered for the next sign-in; nobody signs in to a guest again.
    if (!me.guest) {
      serverPrefs().devices[me.username] = { deviceId, publicKey };
      savePrefs();
    }
    if (hasStore) await native.sessionSet(s);
    this.me = me;
    this.startSession(s);
  }

  async logout(forced = false): Promise<void> {
    const wasToken = api.authed;
    this.gw?.stop();
    this.gw = null;
    if (!forced && wasToken) {
      try {
        await api.post("/auth/logout");
      } catch {
        // already invalid or offline: the local session is dropped anyway
      }
    }
    api.token = null;
    api.cookieSession = false;
    this.session = null;
    if (hasStore) await native.sessionClear(api.base).catch(() => {});
    this.reset();
    this.authNotice = forced ? "conn.loggedOut" : null;
    this.phase = "auth";
  }

  /** Drops all server state; listeners get a local "session_end" event
   *  (logout or server switch) so e.g. voice leaves its call. */
  private reset(): void {
    for (const l of this.listeners) l("session_end", null);
    this.me = null;
    this.deviceId = null;
    this.spaces = {};
    this.spaceOrder = [];
    this.channels = {};
    this.users = {};
    this.readStates = {};
    this.presences = {};
    this.voiceStates = {};
    this.relationships = {};
    this.members = {};
    this.notify = {};
    this.synced = {};
    this.channelId = null;
    this.spaceId = HOME;
  }

  nudge(): void {
    this.gw?.nudge();
  }

  send(op: string, d: unknown): void {
    this.gw?.send(op, d);
  }

  // ---------- navigation ----------

  selectSpace(id: ID): void {
    this.spaceId = id;
    const sp = serverPrefs();
    sp.lastSpace = id;
    const last = sp.lastChannel[id];
    if (last && this.channels[last]) {
      this.channelId = last;
    } else if (id === HOME) {
      this.channelId = null; // friends view
    } else {
      this.channelId = this.spaceChannels(id).find((c) => c.kind === "text")?.id ?? null;
    }
    savePrefs();
    if (id !== HOME) void this.loadMembers(id);
  }

  selectChannel(id: ID): void {
    const c = this.channels[id];
    if (!c) return;
    if (c.kind === "voice" || c.kind === "category") return; // voice joins in phase 4
    const sid = c.spaceId ?? HOME;
    const sp = serverPrefs();
    if (sid !== this.spaceId) {
      this.spaceId = sid;
      sp.lastSpace = sid;
      if (sid !== HOME) void this.loadMembers(sid);
    }
    this.channelId = id;
    sp.lastChannel[sid] = id;
    savePrefs();
  }

  /** Home → friends view (no channel selected). */
  showFriends(): void {
    this.spaceId = HOME;
    this.channelId = null;
    const sp = serverPrefs();
    sp.lastSpace = HOME;
    delete sp.lastChannel[HOME];
    savePrefs();
  }

  /** Opens (or creates) a DM / group DM with the given users and shows it. */
  async openDM(userIds: ID[], name?: string): Promise<void> {
    const c = await api.post<Channel>("/dms", { userIds, name });
    this.upsertChannel(c);
    this.selectChannel(c.id);
  }

  async leaveDM(id: ID): Promise<void> {
    await api.del(`/dms/${id}`);
    this.removeChannel(id);
  }

  async addFriend(username: string): Promise<Relationship["kind"]> {
    const r = await api.post<{ userId: ID; kind: Relationship["kind"] }>("/relationships", { username });
    return r.kind;
  }

  async setRelation(userId: ID, kind: "friend" | "blocked"): Promise<void> {
    await api.put(`/relationships/${userId}`, { kind });
  }

  async removeRelation(userId: ID): Promise<void> {
    await api.del(`/relationships/${userId}`);
  }

  openSearch(query?: string): void {
    this.searchOpen = true;
    if (query) {
      this.searchQuery = query;
      this.searchNonce++;
    }
  }

  /** Shows a message in its channel (ChatView scrolls to it). */
  jumpTo(channelId: ID, messageId: ID): void {
    this.selectChannel(channelId);
    this.jumpTarget = { channelId, messageId };
  }

  /** Alt+Up/Down through the visible text channels of the current scope. */
  moveChannel(delta: number): void {
    const list =
      this.spaceId === HOME ? this.dms() : this.spaceChannels(this.spaceId).filter((c) => c.kind === "text");
    if (!list.length) return;
    const i = list.findIndex((c) => c.id === this.channelId);
    const next = list[(i + delta + list.length) % list.length];
    if (next) this.selectChannel(next.id);
  }

  async loadMembers(spaceId: ID, force = false): Promise<void> {
    if ((!force && this.members[spaceId]) || this.membersLoading[spaceId]) return;
    this.membersLoading[spaceId] = true;
    try {
      const list = await api.get<Member[]>(`/spaces/${spaceId}/members`);
      this.members[spaceId] = list;
      for (const m of list) {
        if (m.userId !== this.me?.id) this.users[m.userId] = m.user;
        if (m.presence) this.applyPresence(m.presence);
      }
    } catch (e) {
      console.warn("members", errorCode(e));
    } finally {
      this.membersLoading[spaceId] = false;
    }
  }

  /** Other users' presence; the own status lives in `me`. */
  private applyPresence(p: Presence): void {
    if (p.userId === this.me?.id) return;
    if (p.status === "offline") delete this.presences[p.userId];
    else this.presences[p.userId] = p;
  }

  setStatus(status: Exclude<Status, "offline">, statusText?: string): void {
    if (!this.me) return;
    this.me.status = status;
    if (statusText !== undefined) this.me.statusText = statusText;
    this.send("presence_set", { status, statusText: this.me.statusText });
  }

  // ---------- gateway events ----------

  private onEvent(op: string, d: unknown): void {
    const h = (this.handlers as Record<string, ((d: never) => void) | undefined>)[op];
    if (h) h(d as never);
    for (const l of this.listeners) l(op, d);
  }

  private listeners: ((op: string, d: unknown) => void)[] = [];

  /** Subscribes other stores (messages, voice) to gateway events. */
  on(fn: (op: string, d: unknown) => void): void {
    this.listeners.push(fn);
  }

  private applyReady(r: Ready): void {
    this.limits = readLimits(r.limits);
    setErrorParams(limitParams(this.limits));
    this.me = r.user;
    this.deviceId = r.deviceId;
    const spaces: Record<ID, Space> = {};
    for (const s of r.spaces) spaces[s.id] = s;
    this.spaces = spaces;
    this.spaceOrder = r.spaces.map((s) => s.id);
    const channels: Record<ID, Channel> = {};
    for (const c of r.channels) channels[c.id] = c;
    for (const c of r.dms) channels[c.id] = c;
    this.channels = channels;
    const users: Record<ID, User> = { ...this.users };
    for (const u of r.users) users[u.id] = u;
    this.users = users;
    const rs: Record<ID, ReadState> = {};
    for (const x of r.readStates) rs[x.channelId] = x;
    this.readStates = rs;
    const pr: Record<ID, Presence> = {};
    for (const p of r.presences) pr[p.userId] = p;
    this.presences = pr;
    const vs: Record<ID, VoiceState> = {};
    for (const v of r.voiceStates) if (v.channelId) vs[v.userId] = v;
    this.voiceStates = vs;
    const rel: Record<ID, Relationship> = {};
    for (const x of r.relationships) rel[x.userId] = x;
    this.relationships = rel;
    this.members = {};
    this.applySynced(r.settings);

    if (r.minClientVersion && versionLess(this.info.version, r.minClientVersion)) this.updateRequired = true;

    // Restore navigation (or fall back to the first space / DMs).
    const keep = this.spaceId !== HOME && this.spaces[this.spaceId] ? this.spaceId : null;
    const lastSpace = serverPrefs().lastSpace;
    const target = keep ?? (lastSpace && (lastSpace === HOME || this.spaces[lastSpace]) ? lastSpace : null);
    const sid = target ?? this.spaceOrder[0] ?? HOME;
    if (
      this.channelId &&
      this.channels[this.channelId] &&
      (this.channels[this.channelId]?.spaceId ?? HOME) === sid
    ) {
      this.spaceId = sid;
      if (sid !== HOME) void this.loadMembers(sid, true);
    } else {
      this.selectSpace(sid);
    }
  }

  private upsertChannel(c: Channel): void {
    // The server omits empty optional fields: a full space channel clears
    // what it no longer carries (limit removed, rules switched off).
    const cleared: Partial<Channel> = c.spaceId
      ? {
          userLimit: undefined,
          mode: undefined,
          slowmode: undefined,
          awayAfter: undefined,
          audio: undefined,
          overrides: undefined,
          icon: undefined,
        }
      : {};
    this.channels[c.id] = { ...this.channels[c.id], ...cleared, ...c };
  }

  private replaceChannels(spaceId: ID, list: Channel[]): void {
    const keep = new Set(list.map((c) => c.id));
    for (const c of Object.values(this.channels))
      if (c.spaceId === spaceId && !keep.has(c.id)) this.removeChannel(c.id);
    for (const c of list) this.upsertChannel(c);
  }

  private removeChannel(id: ID): void {
    delete this.channels[id];
    if (this.channelId === id) this.selectSpace(this.spaceId);
  }

  private removeSpace(id: ID): void {
    delete this.spaces[id];
    this.spaceOrder = this.spaceOrder.filter((x) => x !== id);
    for (const c of Object.values(this.channels)) if (c.spaceId === id) delete this.channels[c.id];
    delete this.members[id];
    if (this.spaceId === id) this.selectSpace(this.spaceOrder[0] ?? HOME);
  }

  private handlers = {
    ready: (d: Ready) => this.applyReady(d),
    space_create: (d: { space: Space; channels: Channel[] }) => {
      this.spaces[d.space.id] = d.space;
      if (!this.spaceOrder.includes(d.space.id)) this.spaceOrder = [...this.spaceOrder, d.space.id];
      for (const c of d.channels) this.upsertChannel(c);
    },
    space_update: (d: Space) => {
      const cur = this.spaces[d.id];
      if (!cur) return;
      // Ownership changes are announced to everyone (also when the
      // server admin changed it).
      if (d.ownerId && d.ownerId !== cur.ownerId) {
        const space = d.name ?? cur.name;
        toast(
          d.ownerId === this.me?.id
            ? t("space.ownerChangedYou", { space })
            : t("space.ownerChanged", { name: this.user(d.ownerId)?.displayName ?? "?", space }),
        );
      }
      this.spaces[d.id] = { ...cur, ...d };
    },
    space_roles: (d: { spaceId: ID; roles: SpaceRole[]; everyonePerms: number }) => {
      const sp = this.spaces[d.spaceId];
      if (!sp) return;
      sp.roles = d.roles;
      sp.everyonePerms = d.everyonePerms;
      // Deleted roles disappear from members too (member_update follows).
      const ids = new Set(d.roles.map((r) => r.id));
      sp.roleIds = (sp.roleIds ?? []).filter((id) => ids.has(id));
    },
    space_delete: (d: { id: ID }) => this.removeSpace(d.id),
    // Space channels arrive bare; new DMs as {channel, users}.
    channel_create: (d: Channel | { channel: Channel; users: User[] }) => {
      if ("channel" in d) {
        for (const u of d.users ?? []) if (u.id !== this.me?.id) this.users[u.id] = u;
        this.upsertChannel(d.channel);
      } else {
        this.upsertChannel(d);
      }
    },
    channel_update: (d: Channel) => this.upsertChannel(d),
    channel_delete: (d: { id: ID }) => this.removeChannel(d.id),
    // The server sends each member only the channels they may see
    //: the list replaces the space's channels.
    channels_reorder: (d: { spaceId: ID; channels: Channel[] }) =>
      this.replaceChannels(d.spaceId, d.channels),
    space_channels: (d: { spaceId: ID; channels: Channel[] }) => this.replaceChannels(d.spaceId, d.channels),
    member_add: (d: Member) => {
      if (d.userId !== this.me?.id) this.users[d.userId] = d.user;
      const list = this.members[d.spaceId];
      if (list && !list.some((m) => m.userId === d.userId)) this.members[d.spaceId] = [...list, d];
    },
    member_update: (d: Member) => {
      const list = this.members[d.spaceId];
      if (list) this.members[d.spaceId] = list.map((m) => (m.userId === d.userId ? { ...m, ...d } : m));
      if (d.userId === this.me?.id) {
        const sp = this.spaces[d.spaceId];
        if (sp) {
          sp.role = d.role;
          sp.roleIds = d.roleIds ?? [];
        }
      }
    },
    member_remove: (d: { spaceId: ID; userId: ID }) => {
      if (d.userId === this.me?.id) {
        this.removeSpace(d.spaceId);
        return;
      }
      const list = this.members[d.spaceId];
      if (list) this.members[d.spaceId] = list.filter((m) => m.userId !== d.userId);
    },
    message_create: (d: {
      channelId: ID;
      id: ID;
      authorId: ID;
      mentions?: ID[];
      mentionsEveryone?: boolean;
    }) => {
      const c = this.channels[d.channelId];
      if (c && idGt(d.id, c.lastMessageId)) c.lastMessageId = d.id;
      if (d.authorId === this.me?.id) {
        this.readStates[d.channelId] = { channelId: d.channelId, lastReadId: d.id, mentionCount: 0 };
      } else if (
        this.me &&
        (d.mentions?.includes(this.me.id) || d.mentionsEveryone || c?.kind === "dm" || c?.kind === "group_dm")
      ) {
        const rs = this.readStates[d.channelId] ?? {
          channelId: d.channelId,
          lastReadId: "0",
          mentionCount: 0,
        };
        this.readStates[d.channelId] = { ...rs, mentionCount: rs.mentionCount + 1 };
      }
    },
    settings_update: (d: { settings: unknown }) => this.applySynced(d.settings),
    read_state_update: (d: ReadState) => {
      this.readStates[d.channelId] = d;
    },
    presence_update: (d: Presence) => this.applyPresence(d),
    voice_state_update: (d: VoiceState) => {
      if (d.channelId) this.voiceStates[d.userId] = d;
      else delete this.voiceStates[d.userId];
    },
    user_update: (d: User | Me) => {
      if (d.id === this.me?.id) this.me = { ...this.me, ...d };
      else this.users[d.id] = { ...this.users[d.id], ...d };
      for (const list of Object.values(this.members)) {
        for (const m of list) if (m.userId === d.id) m.user = { ...m.user, ...d };
      }
    },
    relationship_update: (d: {
      userId: ID;
      kind: Relationship["kind"] | "none";
      user?: User;
      presence?: Presence;
    }) => {
      if (d.presence) this.applyPresence(d.presence);
      if (d.kind === "none") {
        delete this.relationships[d.userId];
        return;
      }
      const user = d.user ?? this.users[d.userId];
      if (user) {
        this.users[d.userId] = user;
        this.relationships[d.userId] = { userId: d.userId, kind: d.kind, user };
      }
    },
  };
}

/** Compares dotted versions ("0.1.10" vs "0.2.0"). */
export function versionLess(a: string, b: string): boolean {
  const pa = a.split(".").map((x) => parseInt(x, 10) || 0);
  const pb = b.split(".").map((x) => parseInt(x, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? 0;
    const y = pb[i] ?? 0;
    if (x !== y) return x < y;
  }
  return false;
}

export const app = new AppState();
if (import.meta.env.DEV) Object.assign(globalThis, { __lvApp: app, __lvApi: api });
