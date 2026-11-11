<script lang="ts">
  // Settings → Privacy & security: E2EE status, own device fingerprint,
  // safety numbers with contacts (compare in person or by phone) and the
  // blocked users.
  import Lock from "@lucide/svelte/icons/lock";
  import { onMount } from "svelte";
  import type { ID } from "../../lib/api/types";
  import { errorCode } from "../../lib/api/client";
  import { app, api } from "../../lib/state/app.svelte";
  import { t, terr } from "../../lib/i18n/index.svelte";
  import { native, hasStore } from "../../lib/native";
  import Avatar from "../../lib/ui/Avatar.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import { toast } from "../../lib/ui/toast.svelte";

  interface DevicesResp {
    devices: { deviceId: ID; userId: ID; publicKey: string }[];
  }

  let myKey = $state<string | null>(null);
  let myFp = $state("");
  let open = $state<Record<ID, string[] | "loading">>({});

  const contacts = $derived(
    app
      .dms()
      .filter((c) => c.kind === "dm")
      .map((c) => ({ dm: c, peer: app.dmPeer(c) }))
      .filter((x) => x.peer),
  );
  const blocked = $derived(Object.values(app.relationships).filter((r) => r.kind === "blocked"));

  onMount(async () => {
    if (!hasStore) return;
    myKey = await native.deviceKey(api.base, false).catch(() => null);
    if (myKey) myFp = await native.keyFingerprint(myKey).catch(() => "");
  });

  /** 60-digit safety number: both 30-digit fingerprints, lower user id first. */
  async function safety(peerId: ID, peerKey: string): Promise<string> {
    const theirs = await native.keyFingerprint(peerKey);
    const me = app.me!.id;
    const pair = BigInt(me) < BigInt(peerId) ? [myFp, theirs] : [theirs, myFp];
    return pair.join(" ");
  }

  async function show(dmId: ID, peerId: ID) {
    if (open[dmId]) {
      delete open[dmId];
      return;
    }
    open[dmId] = "loading";
    try {
      const r = await api.get<DevicesResp>(`/e2ee/${dmId}/devices`);
      const keys = r.devices.filter((d) => d.userId === peerId && d.publicKey);
      open[dmId] = await Promise.all(keys.map((d) => safety(peerId, d.publicKey)));
    } catch (e) {
      delete open[dmId];
      toast(terr(errorCode(e)), "error");
    }
  }

  const groups = (s: string) => s.split(" ");
</script>

<div class="group">
  <div class="row">
    <span class="two">
      <span class="e2ee"><Lock size={13} strokeWidth={2.25} />{t("e2ee.title")}</span>
      <small>{t("e2ee.explain")}</small>
    </span>
  </div>
  <div class="row">
    <span class="two">
      {t("e2ee.myDevice")}
      <small>{app.info.computerName || t("e2ee.thisDevice")}</small>
    </span>
    <code class="fp selectable">{myFp || "–"}</code>
  </div>
</div>

<p class="gh">{t("e2ee.safetyNumbers")}</p>
<div class="group">
  {#each contacts as { dm, peer } (dm.id)}
    {@const sn = open[dm.id]}
    <div class="row">
      <span class="who">
        <Avatar
          name={peer!.displayName}
          id={peer!.id}
          src={peer!.avatarUrl && api.url(peer!.avatarUrl)}
          size={24}
        />
        {peer!.displayName}
      </span>
      <Button variant="plain" busy={sn === "loading"} onclick={() => void show(dm.id, peer!.id)}>
        {sn && sn !== "loading" ? t("common.close") : t("e2ee.show")}
      </Button>
    </div>
    {#if sn && sn !== "loading"}
      <div class="row sn-row">
        {#each sn as num, i (i)}
          <div class="sn selectable" aria-label={t("e2ee.safetyNumberOf", { name: peer!.displayName })}>
            {#each groups(num) as g, j (j)}<span>{g}</span>{/each}
          </div>
        {:else}
          <small>{t("e2ee.noDevices")}</small>
        {/each}
      </div>
    {/if}
  {:else}
    <div class="row"><small>{t("e2ee.noContacts")}</small></div>
  {/each}
</div>
<p class="note">{t("e2ee.safetyHint")}</p>

<p class="gh">{t("privacy.blocked")}</p>
<div class="group">
  {#each blocked as r (r.userId)}
    {@const u = app.user(r.userId)}
    <div class="row">
      <span class="who">
        <Avatar
          name={u?.displayName ?? "?"}
          id={r.userId}
          src={u?.avatarUrl && api.url(u.avatarUrl)}
          size={24}
        />
        {u?.displayName ?? t("common.unknownUser")}
      </span>
      <Button
        variant="plain"
        onclick={() => app.removeRelation(r.userId).catch((e: unknown) => toast(terr(errorCode(e)), "error"))}
        >{t("user.unblock")}</Button
      >
    </div>
  {:else}
    <div class="row"><small>{t("privacy.noneBlocked")}</small></div>
  {/each}
</div>

<style>
  .e2ee {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-weight: 600;
  }
  .e2ee :global(svg) {
    color: var(--success-text);
  }
  .fp {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: var(--fs-callout);
    color: var(--text-2);
  }
  .who {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .sn-row {
    flex-direction: column;
    align-items: flex-start;
  }
  .sn {
    display: grid;
    grid-template-columns: repeat(6, auto);
    gap: 4px 14px;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: var(--fs-body);
    letter-spacing: 0.04em;
  }
</style>
