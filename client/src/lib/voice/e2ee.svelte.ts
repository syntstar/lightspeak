// Media-key management for end-to-end encrypted calls (see
// docs/SECURITY.md). Keys live only in memory; the server stores sealed
// envelopes (X25519 sealed boxes, opened in Rust or – in the browser –
// with a non-extractable WebCrypto key) and never sees a key.
//
//  - key(container, epoch): own envelope → open; none yet → create epoch key
//    and envelopes for every member device; someone else's turn → wait for
//    `e2ee_key_available` (no unencrypted fallback, ever).
//  - e2ee_key_request: share the key with devices that lack an envelope.
//  - e2ee_rotate_request / pending rotations: create epoch+1 for the
//    remaining members (after a kick, ban, leave or device revocation).
import { KeySession } from "./keySession";
import { ApiError, errorCode } from "../api/client";
import type { ID } from "../api/types";
import { native, hasStore } from "../native";
import { app, api } from "../state/app.svelte";
import { savePrefs, serverPrefs } from "../prefs.svelte";
import { t } from "../i18n/index.svelte";
import { toast } from "../ui/toast.svelte";

interface EnvelopeResp {
  containerId: ID;
  epoch: number;
  currentEpoch: number;
  ciphertext: string;
}

interface DevicesResp {
  containerId: ID;
  epoch: number;
  devices: { deviceId: ID; userId: ID; publicKey: string; hasEnvelope: boolean }[];
}

const WAIT_MS = 45_000;

function b64ToBytes(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const jitter = (min: number, max: number) => sleep(min + Math.random() * (max - min));

class KeyManager {
  private lifetime = new AbortController();
  private owner: { session: object | null; server: string } | null = null;

  private scope(): KeySession {
    const session = app.sessionIdentity;
    if (this.owner?.session !== session || this.owner?.server !== api.base) {
      this.forget();
      this.owner = { session, server: api.base };
    }
    return new KeySession(api, () => app.sessionIdentity === session, this.lifetime.signal);
  }
  /** containerId → epoch → raw 32-byte key (base64, memory only). */
  private keys = new Map<ID, Map<number, string>>();
  private waiters = new Map<string, (() => void)[]>();
  /** Containers currently waiting for another member to share the key. */
  waiting = $state<Record<ID, boolean>>({});
  private epochListeners: ((containerId: ID, epoch: number) => void)[] = [];
  /** Newest epoch announced per container (catches rotations during a join). */
  private latest = new Map<ID, number>();

  latestEpoch(cid: ID): number {
    return this.latest.get(cid) ?? 0;
  }

  constructor() {
    app.on((op, d) => this.onEvent(op, d));
  }

  /** Called when a container switched to a new epoch (rotation). */
  onEpochChanged(fn: (containerId: ID, epoch: number) => void): void {
    this.epochListeners.push(fn);
  }

  private cache(cid: ID, epoch: number, key: string, scope: KeySession): void {
    scope.check();
    let m = this.keys.get(cid);
    if (!m) this.keys.set(cid, (m = new Map()));
    m.set(epoch, key);
    // Keep only this and the previous epoch.
    for (const e of [...m.keys()]) if (e < epoch - 1) m.delete(e);
  }

  forget(): void {
    this.owner = null;
    this.lifetime.abort();
    this.lifetime = new AbortController();
    this.keys.clear();
    this.latest.clear();
    for (const fs of this.waiters.values()) for (const done of fs) done();
    this.waiters.clear();
    this.waiting = {};
  }

  /**
   * Drops the cached key and fetches the envelope again (self-healing when a
   * peer cannot be decrypted: our cached key may be stale). Afterwards
   * latestEpoch() reflects the server's current epoch.
   */
  async refresh(cid: ID, epoch: number): Promise<Uint8Array> {
    this.keys.get(cid)?.delete(epoch);
    return this.key(cid, epoch);
  }

  /** Raw media key for a container/epoch; throws ApiError on failure. */
  async key(cid: ID, epoch: number): Promise<Uint8Array> {
    const scope = this.scope();
    const key = await scope.run(() => this.keyB64(cid, epoch));
    scope.check();
    return b64ToBytes(key);
  }

  private async keyB64(cid: ID, epoch: number): Promise<string> {
    const scope = this.scope();
    const hit = this.keys.get(cid)?.get(epoch);
    if (hit) return hit;
    if (!hasStore) throw new ApiError("device_key_missing", 0);
    const deadline = Date.now() + WAIT_MS;
    for (;;) {
      try {
        const r = await scope.get<EnvelopeResp>(`/e2ee/${cid}/envelope?epoch=${epoch}`);
        const key = await scope.run(() => native.e2eeOpen(scope.api.base, r.ciphertext));
        this.cache(cid, epoch, key, scope);
        if (r.currentEpoch > this.latestEpoch(cid)) this.latest.set(cid, r.currentEpoch);
        delete this.waiting[cid];
        return key;
      } catch (e) {
        scope.check();
        const code = errorCode(e);
        if (code === "no_key_yet") {
          const key = await this.createKey(cid, epoch, scope);
          if (key) return key;
          continue; // somebody else was faster: fetch theirs
        }
        if (code !== "envelope_missing" && code !== "key_exists") {
          delete this.waiting[cid];
          throw e;
        }
        if (Date.now() > deadline) {
          delete this.waiting[cid];
          throw new ApiError("e2ee_key_timeout", 0);
        }
        this.waiting[cid] = true;
        await scope.run(() => this.waitForKey(cid, epoch, 5000));
        // Nobody could share this epoch and the server had it rotated: the
        // caller moves on to the new one instead of waiting for the old.
        if (this.latestEpoch(cid) > epoch) {
          delete this.waiting[cid];
          throw new ApiError("epoch_superseded", 0);
        }
      }
    }
  }

  private waitForKey(cid: ID, epoch: number, ms: number): Promise<void> {
    const k = `${cid}:${epoch}`;
    return new Promise((resolve) => {
      const done = () => {
        clearTimeout(t);
        resolve();
      };
      const t = setTimeout(() => {
        this.waiters.set(
          k,
          (this.waiters.get(k) ?? []).filter((f) => f !== done),
        );
        resolve();
      }, ms);
      this.waiters.set(k, [...(this.waiters.get(k) ?? []), done]);
    });
  }

  /** Bootstraps the first key of an epoch; null when another device won. */
  private async createKey(cid: ID, epoch: number, scope: KeySession): Promise<string | null> {
    const key = await scope.run(() => native.e2eeNewKey());
    const devs = await scope.get<DevicesResp>(`/e2ee/${cid}/devices`);
    if (devs.epoch !== epoch) return null;
    const envelopes = await this.seal(devs.devices, key, scope);
    try {
      await scope.post(`/e2ee/${cid}/envelopes`, { epoch, envelopes });
      this.cache(cid, epoch, key, scope);
      return key;
    } catch (e) {
      if (["key_exists", "epoch_conflict"].includes(errorCode(e))) return null;
      throw e;
    }
  }

  /**
   * Trust on first use: remembers contacts' device keys and tells the user
   * when a known contact shows up with a key we have not seen before (new
   * device or reinstall). Membership is authorised by the server; this makes
   * a key injected by a compromised server visible.
   */
  private checkKeys(devices: DevicesResp["devices"]): void {
    const me = app.me?.id;
    let dirty = false;
    for (const d of devices) {
      if (!d.publicKey || d.userId === me) continue;
      const known = serverPrefs().knownKeys[d.userId];
      if (known?.includes(d.publicKey)) continue;
      if (known) {
        const name = app.user(d.userId)?.displayName ?? t("common.unknownUser");
        toast(t("e2ee.newDevice", { name }), "info", 8000);
      }
      serverPrefs().knownKeys[d.userId] = [...(known ?? []), d.publicKey].slice(-20);
      dirty = true;
    }
    if (dirty) savePrefs();
  }

  private async seal(devices: DevicesResp["devices"], key: string, scope: KeySession) {
    scope.check();
    this.checkKeys(devices);
    const out: { deviceId: ID; ciphertext: string }[] = [];
    for (const d of devices) {
      if (!d.publicKey) continue;
      try {
        out.push({
          deviceId: d.deviceId,
          ciphertext: await scope.run(() => native.e2eeSeal(d.publicKey, key)),
        });
      } catch {
        scope.check();
        // invalid key of one device must not block the others
      }
    }
    scope.check();
    return out;
  }

  /** Shares our key with devices that have no envelope yet. */
  private async share(cid: ID, epoch: number, deviceIds: ID[]): Promise<void> {
    const scope = this.scope();
    await scope.run(() => jitter(150, 1500)); // let one member answer, the rest find nothing to do
    let key: string;
    try {
      key = await this.ownKeyNoCreate(cid, epoch, scope);
    } catch {
      return; // we have no key for this epoch either
    }
    const devs = await scope.get<DevicesResp>(`/e2ee/${cid}/devices`);
    if (devs.epoch !== epoch) return;
    const want = new Set(deviceIds);
    const missing = devs.devices.filter((d) => !d.hasEnvelope && (want.size === 0 || want.has(d.deviceId)));
    if (!missing.length) return;
    const envelopes = await this.seal(missing, key, scope);
    if (envelopes.length) await scope.post(`/e2ee/${cid}/envelopes`, { epoch, envelopes }).catch(() => {});
  }

  private async ownKeyNoCreate(cid: ID, epoch: number, scope: KeySession): Promise<string> {
    const hit = this.keys.get(cid)?.get(epoch);
    if (hit) return hit;
    const r = await scope.get<EnvelopeResp>(`/e2ee/${cid}/envelope?epoch=${epoch}`);
    const key = await scope.run(() => native.e2eeOpen(scope.api.base, r.ciphertext));
    this.cache(cid, epoch, key, scope);
    return key;
  }

  /** Creates epoch+1 for the current members (rotation after removal). */
  private async rotate(cid: ID): Promise<void> {
    const scope = this.scope();
    if (!hasStore) return;
    await scope.run(() => jitter(300, 3000));
    const devs = await scope.get<DevicesResp>(`/e2ee/${cid}/devices`);
    const newEpoch = devs.epoch + 1;
    const key = await scope.run(() => native.e2eeNewKey());
    const envelopes = await this.seal(devs.devices, key, scope);
    try {
      await scope.post(`/e2ee/${cid}/rotate`, { newEpoch, envelopes });
      this.cache(cid, newEpoch, key, scope);
    } catch (e) {
      // Another member rotated first (rotation_not_pending / epoch_conflict).
      if (!["epoch_conflict", "forbidden", "rotation_not_pending"].includes(errorCode(e)))
        console.warn("e2ee rotate", errorCode(e));
    }
  }

  private onEvent(op: string, d: unknown): void {
    switch (op) {
      case "session_end":
        this.forget();
        break;
      case "ready":
        for (const cid of (d as { pendingRotations?: ID[] }).pendingRotations ?? [])
          void this.rotate(cid).catch(() => {});
        break;
      case "e2ee_key_available": {
        const x = d as { containerId: ID; epoch: number };
        const k = `${x.containerId}:${x.epoch}`;
        for (const f of this.waiters.get(k) ?? []) f();
        this.waiters.delete(k);
        break;
      }
      case "e2ee_key_request": {
        const x = d as { containerId: ID; epoch: number; deviceIds: ID[] };
        void this.share(x.containerId, x.epoch, x.deviceIds).catch(() => {});
        break;
      }
      case "e2ee_rotate_request":
        void this.rotate((d as { containerId: ID }).containerId).catch(() => {});
        break;
      case "e2ee_epoch_changed": {
        const x = d as { containerId: ID; epoch: number };
        this.latest.set(x.containerId, Math.max(this.latest.get(x.containerId) ?? 0, x.epoch));
        // Wake fetches still waiting for an older epoch (they re-check).
        for (const [k, fs] of this.waiters) {
          const [cid, ep] = k.split(":");
          if (cid !== x.containerId || Number(ep) >= x.epoch) continue;
          this.waiters.delete(k);
          for (const f of fs) f();
        }
        for (const f of this.epochListeners) f(x.containerId, x.epoch);
        break;
      }
    }
  }
}

export const keys = new KeyManager();
