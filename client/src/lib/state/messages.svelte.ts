// Message cache per channel (LRU: 5 channels × 200 messages),
// optimistic sending with nonce, typing indicators, read acks.
import { errorCode } from "../api/client";
import type { Attachment, ID, Message } from "../api/types";
import { app, api } from "./app.svelte";
import { idCmp, idGt } from "./ids";

export const MAX_CHANNELS = 5;
export const MAX_PER_CHANNEL = 200;
export const PAGE = 50;

export interface ChatMessage extends Message {
  /** Local state of an optimistic message. */
  pending?: boolean;
  failed?: string;
}

interface ChannelCache {
  msgs: ChatMessage[];
  /** Older messages exist on the server. */
  hasMore: boolean;
  /** Newer messages were trimmed from the cache (scrolled far up). */
  loading: boolean;
  error: string | null;
}

class MessageStore {
  channels = $state<Record<ID, ChannelCache>>({});
  typing = $state<Record<ID, Record<ID, number>>>({});
  private lru: ID[] = [];
  private ackTimer: ReturnType<typeof setTimeout> | undefined;
  private lastTyping = 0;

  constructor() {
    app.on((op, d) => this.onEvent(op, d));
    setInterval(() => this.expireTyping(), 2000);
  }

  get(id: ID): ChannelCache | undefined {
    return this.channels[id];
  }

  private touch(id: ID): void {
    this.lru = [id, ...this.lru.filter((x) => x !== id)];
    for (const old of this.lru.splice(MAX_CHANNELS)) delete this.channels[old];
  }

  async open(id: ID): Promise<void> {
    this.touch(id);
    if (this.channels[id]) return;
    this.channels[id] = { msgs: [], hasMore: true, loading: true, error: null };
    try {
      const msgs = await api.get<Message[]>(`/channels/${id}/messages?limit=${PAGE}`);
      const c = this.channels[id];
      if (!c) return;
      // Live events may have arrived during the fetch: merge.
      c.msgs = mergeSorted(msgs, c.msgs);
      c.hasMore = msgs.length >= PAGE;
      c.loading = false;
    } catch (e) {
      const c = this.channels[id];
      if (c) {
        c.loading = false;
        c.error = errorCode(e);
      }
    }
  }

  /** Drops the cache so the next open() reloads (after a reconnect). */
  invalidate(): void {
    this.channels = {};
    this.lru = [];
  }

  async loadOlder(id: ID): Promise<boolean> {
    const c = this.channels[id];
    if (!c || c.loading || !c.hasMore) return false;
    const first = c.msgs.find((m) => !m.pending);
    if (!first) return false;
    c.loading = true;
    try {
      const older = await api.get<Message[]>(`/channels/${id}/messages?before=${first.id}&limit=${PAGE}`);
      c.hasMore = older.length >= PAGE;
      c.msgs = mergeSorted(older, c.msgs);
      if (c.msgs.length > MAX_PER_CHANNEL + PAGE) {
        // Keep memory bounded: drop the newest beyond the window; they are
        // re-fetched when the user scrolls back down (open() after invalidate).
        c.msgs = c.msgs.slice(0, MAX_PER_CHANNEL + PAGE);
      }
      return older.length > 0;
    } catch (e) {
      c.error = errorCode(e);
      return false;
    } finally {
      c.loading = false;
    }
  }

  async send(
    channelId: ID,
    content: string,
    replyTo?: Message,
    attachments: Attachment[] = [],
  ): Promise<void> {
    const me = app.me;
    if (!me) return;
    const nonce = crypto.randomUUID();
    const c = this.channels[channelId];
    const temp: ChatMessage = {
      id: `pending-${nonce}`,
      channelId,
      authorId: me.id,
      author: me,
      content,
      replyToId: replyTo?.id,
      referencedMessage: replyTo
        ? { id: replyTo.id, authorId: replyTo.authorId, content: replyTo.content }
        : undefined,
      mentions: [],
      mentionsEveryone: false,
      attachments,
      reactions: [],
      pinned: false,
      createdAt: Date.now(),
      nonce,
      pending: true,
    };
    c?.msgs.push(temp);
    await this.deliver(temp);
  }

  async retry(m: ChatMessage): Promise<void> {
    m.failed = undefined;
    m.pending = true;
    await this.deliver(m);
  }

  discard(m: ChatMessage): void {
    const c = this.channels[m.channelId];
    if (c) c.msgs = c.msgs.filter((x) => x.nonce !== m.nonce || !x.pending);
  }

  private async deliver(temp: ChatMessage): Promise<void> {
    try {
      const real = await api.post<Message>(`/channels/${temp.channelId}/messages`, {
        content: temp.content,
        replyToId: temp.replyToId,
        attachmentIds: temp.attachments.map((a) => a.id),
        nonce: temp.nonce,
      });
      this.upsert(real);
    } catch (e) {
      const c = this.channels[temp.channelId];
      const m = c?.msgs.find((x) => x.nonce === temp.nonce && x.pending);
      if (m) m.failed = errorCode(e);
    }
  }

  async edit(m: Message, content: string): Promise<void> {
    await api.patch(`/channels/${m.channelId}/messages/${m.id}`, { content });
  }

  async remove(m: Message): Promise<void> {
    await api.del(`/channels/${m.channelId}/messages/${m.id}`);
  }

  async react(m: Message, emoji: string, on: boolean): Promise<void> {
    const path = `/channels/${m.channelId}/messages/${m.id}/reactions/${encodeURIComponent(emoji)}`;
    if (on) await api.put(path);
    else await api.del(path);
  }

  async pin(m: Message, on: boolean): Promise<void> {
    if (on) await api.put(`/channels/${m.channelId}/pins/${m.id}`);
    else await api.del(`/channels/${m.channelId}/pins/${m.id}`);
  }

  /** Throttled typing notification (max 1 per 3 s). */
  typingNow(channelId: ID): void {
    const now = Date.now();
    if (now - this.lastTyping < 3000) return;
    this.lastTyping = now;
    void api.post(`/channels/${channelId}/typing`).catch(() => {});
  }

  typingUsers(channelId: ID): ID[] {
    return Object.keys(this.typing[channelId] ?? {});
  }

  /** Marks the channel as read up to its newest message (debounced). */
  ack(channelId: ID): void {
    const ch = app.channels[channelId];
    const last = ch?.lastMessageId;
    if (!last || !idGt(last, app.readStates[channelId]?.lastReadId ?? "0")) return;
    clearTimeout(this.ackTimer);
    this.ackTimer = setTimeout(() => {
      app.readStates[channelId] = { channelId, lastReadId: last, mentionCount: 0 };
      void api.post(`/channels/${channelId}/ack`, { messageId: last }).catch(() => {});
    }, 800);
  }

  private upsert(m: Message): void {
    const c = this.channels[m.channelId];
    if (!c) return;
    const byNonce = m.nonce ? c.msgs.findIndex((x) => x.nonce === m.nonce && x.pending) : -1;
    if (byNonce >= 0) {
      c.msgs.splice(byNonce, 1);
    }
    const i = c.msgs.findIndex((x) => x.id === m.id);
    if (i >= 0) c.msgs[i] = { ...c.msgs[i], ...m, pending: false };
    else c.msgs = mergeSorted([m], c.msgs);
    // The author stops typing once the message arrives.
    const t = this.typing[m.channelId];
    if (t) delete t[m.authorId];
  }

  private find(channelId: ID, id: ID): ChatMessage | undefined {
    return this.channels[channelId]?.msgs.find((m) => m.id === id);
  }

  private expireTyping(): void {
    const now = Date.now();
    for (const [cid, users] of Object.entries(this.typing)) {
      for (const [uid, until] of Object.entries(users)) if (until < now) delete users[uid];
      if (!Object.keys(users).length) delete this.typing[cid];
    }
  }

  private onEvent(op: string, d: unknown): void {
    switch (op) {
      case "ready":
        this.invalidate();
        break;
      case "message_create":
        this.upsert(d as Message);
        break;
      case "message_update": {
        const u = d as Partial<Message> & { id: ID; channelId: ID };
        const m = this.find(u.channelId, u.id);
        if (m) Object.assign(m, u);
        break;
      }
      case "message_delete": {
        const x = d as { id: ID; channelId: ID };
        const c = this.channels[x.channelId];
        if (c) c.msgs = c.msgs.filter((m) => m.id !== x.id);
        break;
      }
      case "reaction_add":
      case "reaction_remove": {
        const x = d as { channelId: ID; messageId: ID; userId: ID; emoji: string };
        const m = this.find(x.channelId, x.messageId);
        if (!m) break;
        const add = op === "reaction_add";
        const mine = x.userId === app.me?.id;
        const r = m.reactions.find((r) => r.emoji === x.emoji);
        if (r) {
          if (mine && r.me === add) break; // already applied optimistically
          r.count += add ? 1 : -1;
          if (mine) r.me = add;
          if (r.count <= 0) m.reactions = m.reactions.filter((y) => y !== r);
        } else if (add) {
          m.reactions.push({ emoji: x.emoji, count: 1, me: mine });
        }
        break;
      }
      case "channel_pins_update": {
        const x = d as { channelId: ID; messageId: ID; pinned: boolean };
        const m = this.find(x.channelId, x.messageId);
        if (m) m.pinned = x.pinned;
        break;
      }
      case "attachment_delete": {
        const x = d as { id: ID; messageId: ID; channelId: ID };
        const m = this.find(x.channelId, x.messageId);
        if (m) m.attachments = m.attachments.filter((a) => a.id !== x.id);
        break;
      }
      case "typing_start": {
        const x = d as { channelId: ID; userId: ID };
        if (x.userId === app.me?.id) break;
        (this.typing[x.channelId] ??= {})[x.userId] = Date.now() + 8000;
        break;
      }
    }
  }
}

/** Merges two ascending lists by id, de-duplicating (later list wins). */
export function mergeSorted<T extends { id: string; pending?: boolean }>(a: T[], b: T[]): T[] {
  const byId = new Map<string, T>();
  const pending: T[] = [];
  for (const m of [...a, ...b]) {
    if (m.pending) pending.push(m);
    else byId.set(m.id, m);
  }
  const sorted = [...byId.values()].sort((x, y) => idCmp(x.id, y.id));
  return [...sorted, ...pending];
}

export const messages = new MessageStore();
