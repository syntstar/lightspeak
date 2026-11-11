// DM / group-DM calls: ringing (sound + Windows toast with accept/decline,
// also while the app sits in the tray), answering and the 45 s timeout
// (server side). A call is just the DM's voice room; ringing is signalling.
import { untrack } from "svelte";
import type { ID } from "../api/types";
import { errorCode } from "../api/client";
import { t, terr } from "../i18n/index.svelte";
import { native, inTauri, hasStore } from "../native";
import { doNotDisturb, onToastAction } from "../notifier.svelte";
import { prefs } from "../prefs.svelte";
import { app, api } from "../state/app.svelte";
import { toast } from "../ui/toast.svelte";
import { ring } from "./sounds";
import { voice } from "./voice.svelte";

interface Incoming {
  channelId: ID;
  fromUserId: ID;
  expiresAt: number;
}

const tag = (id: ID) => `call-${id}`;

/** A private call with nobody else left ends after this (ringing has its own 45 s). */
export const ALONE_MS = 3 * 60_000;

class Calls {
  /** Calls ringing at us, by DM channel id. */
  incoming = $state<Record<ID, Incoming>>({});
  /** DM we are calling (ringing the others). */
  outgoing = $state<ID | null>(null);

  private stopIncoming: (() => void) | null = null;
  private stopOutgoing: (() => void) | null = null;
  private expiry = new Map<ID, ReturnType<typeof setTimeout>>();
  /** Overridable in dev tests (automated call scenarios). */
  aloneMs = ALONE_MS;
  private aloneIn: ID | null = null;
  private aloneTimer: ReturnType<typeof setTimeout> | undefined;

  constructor() {
    // Alone in a DM call (the others hung up or dropped): leave after
    // ALONE_MS. A partner who only restarts (update) is back long before.
    $effect.root(() => {
      $effect(() => {
        const id = voice.channelId;
        const c = id ? app.channels[id] : undefined;
        const alone =
          !!id &&
          (c?.kind === "dm" || c?.kind === "group_dm") &&
          voice.status === "connected" &&
          !app.voiceMembers(id).some((v) => v.userId !== app.me?.id);
        untrack(() => this.setAlone(alone ? id : null));
      });
    });
    app.on((op, d) => {
      if (op === "call_ring") this.onRing(d as Incoming);
      else if (op === "call_end") this.onEnd(d as { channelId: ID; reason: string; userId?: ID });
      else if (op === "voice_state_update") this.onVoiceState(d as { userId: ID; channelId: ID | null });
    });
    onToastAction((args) => {
      const [kind, action, id] = args.split(":");
      if (kind !== "call" || !id) return false;
      if (action === "accept") void this.accept(id);
      else if (action === "decline") void this.decline(id);
      else if (app.channels[id]) app.selectChannel(id);
      return true;
    });
  }

  /** Starts a call in a DM: join its voice room and ring the others. */
  async start(dmId: ID): Promise<void> {
    await voice.join(dmId);
    if (voice.channelId !== dmId) return;
    const others = app.voiceMembers(dmId).filter((v) => v.userId !== app.me?.id);
    if (others.length) return; // a call is already running: just joined it
    try {
      await api.post(`/calls/${dmId}/ring`);
      this.outgoing = dmId;
      this.stopOutgoing?.();
      this.stopOutgoing = ring(true);
    } catch (e) {
      toast(terr(errorCode(e)), "error");
    }
  }

  async accept(id: ID): Promise<void> {
    this.clearIncoming(id);
    if (inTauri) void native.windowShow().catch(() => {});
    if (app.channels[id]) app.selectChannel(id);
    await voice.join(id);
  }

  async decline(id: ID): Promise<void> {
    this.clearIncoming(id);
    await api.post(`/calls/${id}/decline`).catch(() => {});
  }

  private onRing(d: Incoming): void {
    if (voice.channelId === d.channelId) return; // already in this call
    if (app.relation(d.fromUserId) === "blocked") return;
    this.incoming[d.channelId] = d;
    const ms = Math.max(1000, Math.min(60_000, d.expiresAt - Date.now()));
    clearTimeout(this.expiry.get(d.channelId));
    this.expiry.set(
      d.channelId,
      setTimeout(() => this.clearIncoming(d.channelId), ms),
    );
    this.stopIncoming?.();
    this.stopIncoming = ring(false);
    if (hasStore && prefs.toasts && !doNotDisturb()) {
      const c = app.channels[d.channelId];
      const from = app.user(d.fromUserId)?.displayName ?? t("common.unknownUser");
      const title = c?.kind === "group_dm" ? `${from} · ${app.channelTitle(c)}` : from;
      void native
        .notifyShow({
          title,
          body: t("call.incoming"),
          tag: tag(d.channelId),
          args: `call:open:${d.channelId}`,
          call: true,
          actions: [
            { label: t("call.accept"), args: `call:accept:${d.channelId}` },
            { label: t("call.decline"), args: `call:decline:${d.channelId}` },
          ],
        })
        .catch(() => {});
    }
  }

  private onEnd(d: { channelId: ID; reason: string; userId?: ID }): void {
    if (this.incoming[d.channelId]) {
      // "answered" by one of our other devices, declined (1:1), timed out
      this.clearIncoming(d.channelId);
    }
    if (this.outgoing === d.channelId && d.userId !== app.me?.id) {
      this.stopRinging();
      const alone = !app.voiceMembers(d.channelId).some((v) => v.userId !== app.me?.id);
      if (d.reason === "declined" && alone) {
        toast(t("call.declined"), "info");
        void voice.leave();
      } else if (d.reason === "timeout" && alone) {
        toast(t("call.noAnswer"), "info");
        void voice.leave();
      }
    }
  }

  private onVoiceState(d: { userId: ID; channelId: ID | null }): void {
    // Someone joined our outgoing call: stop the ringback tone.
    if (this.outgoing && d.channelId === this.outgoing && d.userId !== app.me?.id) this.stopRinging();
    // We left the call ourselves.
    if (this.outgoing && d.userId === app.me?.id && d.channelId !== this.outgoing) this.stopRinging();
    // We joined a ringing call from another device/route.
    if (d.userId === app.me?.id && d.channelId && this.incoming[d.channelId]) this.clearIncoming(d.channelId);
  }

  private setAlone(id: ID | null): void {
    if (id === this.aloneIn) return;
    clearTimeout(this.aloneTimer);
    this.aloneIn = id;
    if (!id) return;
    this.aloneTimer = setTimeout(() => {
      if (this.aloneIn !== id || voice.channelId !== id) return;
      this.aloneIn = null;
      toast(t("call.aloneEnded"), "info", 8000);
      void voice.leave();
    }, this.aloneMs);
  }

  private stopRinging(): void {
    this.stopOutgoing?.();
    this.stopOutgoing = null;
    this.outgoing = null;
  }

  private clearIncoming(id: ID): void {
    delete this.incoming[id];
    clearTimeout(this.expiry.get(id));
    this.expiry.delete(id);
    if (hasStore) void native.notifyClear(tag(id)).catch(() => {});
    if (!Object.keys(this.incoming).length) {
      this.stopIncoming?.();
      this.stopIncoming = null;
    }
  }
}

export const calls = new Calls();

if (import.meta.env.DEV) Object.assign(globalThis, { __lvCalls: calls });
