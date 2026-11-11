<script lang="ts">
  // Start a DM (one person) or a group DM (2–9 others) with known users.
  import Check from "@lucide/svelte/icons/check";
  import type { User } from "../lib/api/types";
  import { errorCode } from "../lib/api/client";
  import { app, api } from "../lib/state/app.svelte";
  import { t, terr } from "../lib/i18n/index.svelte";
  import Sheet from "../lib/ui/Sheet.svelte";
  import Button from "../lib/ui/Button.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import TextField from "../lib/ui/TextField.svelte";

  const MAX_OTHERS = 9;
  let { open = $bindable(false) }: { open?: boolean } = $props();

  let filter = $state("");
  let name = $state("");
  let picked = $state<string[]>([]);
  let busy = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    if (open) {
      filter = "";
      name = "";
      picked = [];
      error = null;
      // Members of all spaces are candidates (small groups: cheap, once).
      for (const id of app.spaceOrder) void app.loadMembers(id);
    }
  });

  const candidates = $derived.by((): User[] => {
    const f = filter.trim().toLowerCase();
    // Only friends can be messaged.
    return app
      .knownUsers()
      .filter((u) => app.relation(u.id) === "friend")
      .filter((u) => !f || u.displayName.toLowerCase().includes(f) || u.username.includes(f))
      .sort((a, b) => a.displayName.localeCompare(b.displayName))
      .slice(0, 50);
  });

  function toggle(id: string) {
    if (picked.includes(id)) picked = picked.filter((x) => x !== id);
    else if (picked.length < MAX_OTHERS) picked = [...picked, id];
  }

  async function start() {
    if (!picked.length) return;
    busy = true;
    error = null;
    try {
      await app.openDM(picked, picked.length > 1 ? name.trim() || undefined : undefined);
      open = false;
    } catch (e) {
      error = terr(errorCode(e));
    } finally {
      busy = false;
    }
  }
</script>

<Sheet bind:open title={t("dm.new")} width={440}>
  <p class="sub">{t("dm.newSub", { max: MAX_OTHERS })}</p>
  <TextField bind:value={filter} placeholder={t("dm.filter")} aria-label={t("dm.filter")} autofocus />
  <ul class="list" role="listbox" aria-multiselectable="true" aria-label={t("dm.people")}>
    {#each candidates as u (u.id)}
      {@const on = picked.includes(u.id)}
      <li>
        <button
          type="button"
          role="option"
          aria-selected={on}
          class:on
          disabled={!on && picked.length >= MAX_OTHERS}
          onclick={() => toggle(u.id)}
        >
          <Avatar name={u.displayName} id={u.id} src={u.avatarUrl && api.url(u.avatarUrl)} size={28} />
          <span class="dn">{u.displayName} <span class="un">@{u.username}</span></span>
          <span class="box"
            >{#if on}<Check size={13} strokeWidth={2.5} />{/if}</span
          >
        </button>
      </li>
    {:else}
      <li class="empty">{filter.trim() ? t("qs.empty") : t("dm.noFriends")}</li>
    {/each}
  </ul>
  {#if picked.length > 1}
    <TextField
      bind:value={name}
      label={t("dm.groupName")}
      placeholder={t("common.optional")}
      maxlength={64}
    />
  {/if}
  {#if error}<p class="err" role="alert">{error}</p>{/if}
  {#snippet footer()}
    <Button onclick={() => (open = false)}>{t("common.cancel")}</Button>
    <Button variant="primary" {busy} disabled={!picked.length} onclick={start}>
      {picked.length > 1 ? t("dm.createGroup") : t("dm.start")}
    </Button>
  {/snippet}
</Sheet>

<style>
  .sub {
    margin: -8px 0 0;
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 300px;
    overflow-y: auto;
  }
  .list button {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    height: 40px;
    padding: 0 8px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-1);
    text-align: left;
  }
  .list button:hover:not(:disabled) {
    background: var(--bg-hover);
  }
  .list button:disabled {
    opacity: 0.45;
  }
  .dn {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .un {
    color: var(--text-3);
    font-size: var(--fs-callout);
  }
  .box {
    width: 18px;
    height: 18px;
    border-radius: 5px;
    border: 1.5px solid var(--border-hairline);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
  }
  .on .box {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--on-accent);
  }
  .empty {
    padding: 16px 8px;
    color: var(--text-3);
    font-size: var(--fs-callout);
  }
  .err {
    margin: 0;
    color: var(--danger);
    font-size: var(--fs-callout);
  }
</style>
