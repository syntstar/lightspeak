<script lang="ts">
  // Channel editor → Permissions. Left the roles this channel
  // treats differently (@everyone always first), right the few channel
  // permissions of the selected role: deny, default (space-wide value) or
  // allow. Changes are kept in `value` and saved with the channel; what the
  // current user may not change is disabled, the server checks it again.
  import Plus from "@lucide/svelte/icons/plus";
  import Users from "@lucide/svelte/icons/users";
  import Shield from "@lucide/svelte/icons/shield";
  import type { ID, Override } from "../lib/api/types";
  import { app } from "../lib/state/app.svelte";
  import { t, type MessageKey } from "../lib/i18n/index.svelte";
  import { CHANNEL_PERMS, P, grantable, has, type PermName } from "../lib/perms";
  import { setTri, triOf, type TriState as Tri } from "../lib/channelRules";
  import { accessOf } from "../lib/spaceAdmin.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import Button from "../lib/ui/Button.svelte";
  import TriState from "../lib/ui/TriState.svelte";

  let {
    spaceId,
    kind,
    value = $bindable(),
  }: { spaceId: ID; kind: "text" | "voice"; value: Override[] } = $props();

  const space = $derived(app.spaces[spaceId]);
  const me = $derived(accessOf(spaceId));
  const perms = $derived(CHANNEL_PERMS[kind]);
  /** Roles shown in the list even without a deviation yet (just added). */
  let added = $state<ID[]>([]);
  let picked = $state<ID | null>(null);
  /** Selected role; @everyone (the space ID) by default. */
  const sel = $derived(picked ?? spaceId);

  const listed = $derived.by(() => {
    const ids = new Set([...value.map((o) => o.roleId), ...added]);
    return (space?.roles ?? []).filter((r) => ids.has(r.id));
  });
  const addable = $derived((space?.roles ?? []).filter((r) => !listed.some((l) => l.id === r.id)));
  const isEveryone = $derived(sel === spaceId);
  const selRole = $derived(isEveryone ? null : (space?.roles ?? []).find((r) => r.id === sel));
  // The selection disappears with its role (deleted meanwhile).
  $effect(() => {
    if (!isEveryone && !selRole) picked = spaceId;
  });

  const current = $derived(value.find((o) => o.roleId === sel) ?? { roleId: sel, allow: 0, deny: 0 });
  /** May edit this role's override at all (rank rule). */
  const roleEditable = $derived(
    has(me, P.manageRoles) && (isEveryone || (!!selRole && selRole.position < me.rank)),
  );

  function set(bit: number, v: Tri) {
    const next = setTri(current, bit, v);
    const rest = value.filter((o) => o.roleId !== sel);
    value = next.allow || next.deny ? [...rest, next] : rest;
  }

  function addRole(e: MouseEvent) {
    openMenu(
      e,
      addable.map((r) => ({
        label: r.name,
        disabled: !(has(me, P.manageRoles) && r.position < me.rank),
        action: () => {
          added = [...added, r.id];
          picked = r.id;
        },
      })),
    );
  }

  function removeRole() {
    value = value.filter((o) => o.roleId !== sel);
    added = added.filter((id) => id !== sel);
    picked = spaceId;
  }

  const label = (p: PermName) => t(`chperm.${p}` as MessageKey);
  const hint = (p: PermName) => t(`chperm.${p}.hint` as MessageKey);
  /** For @everyone, "default" means the space-wide value – say which one. */
  function inherited(p: PermName): string {
    const on = ((space?.everyonePerms ?? 0) & P[p]) === P[p];
    return on ? t("chperm.spaceAllows") : t("chperm.spaceDenies");
  }
  const deviations = (id: ID): number => {
    const o = value.find((x) => x.roleId === id);
    if (!o) return 0;
    let n = 0;
    for (const p of perms) if ((o.allow | o.deny) & P[p]) n++;
    return n;
  };
</script>

<div class="pane">
  <nav class="roles" aria-label={t("chperm.roles")}>
    <button
      type="button"
      class="role"
      class:sel={isEveryone}
      aria-current={isEveryone ? "true" : undefined}
      onclick={() => (picked = spaceId)}
    >
      <Users size={14} strokeWidth={1.75} />
      <span class="nm">@everyone</span>
      {#if deviations(spaceId)}<span class="dot" aria-hidden="true"></span>{/if}
    </button>
    {#each listed as r (r.id)}
      <button
        type="button"
        class="role"
        class:sel={sel === r.id}
        aria-current={sel === r.id ? "true" : undefined}
        onclick={() => (picked = r.id)}
      >
        <Shield size={14} strokeWidth={1.75} />
        <span class="nm">{r.name}</span>
        {#if deviations(r.id)}<span class="dot" aria-hidden="true"></span>{/if}
      </button>
    {/each}
    {#if addable.length && has(me, P.manageRoles)}
      <button type="button" class="role add" onclick={addRole}>
        <Plus size={14} strokeWidth={1.75} />
        <span class="nm">{t("chperm.addRole")}</span>
      </button>
    {:else if !(space?.roles ?? []).length}
      <p class="empty">{t("chperm.noRoles")}</p>
    {/if}
  </nav>

  <div class="detail">
    <p class="intro">
      {isEveryone ? t("chperm.everyoneIntro") : t("chperm.roleIntro", { role: selRole?.name ?? "" })}
    </p>
    {#if !roleEditable}<p class="intro">{t("chperm.readOnly")}</p>{/if}
    <div class="group">
      {#each perms as p (p)}
        {@const state = triOf(current, P[p])}
        <div class="perm">
          <span class="two">
            {label(p)}
            <small>
              {hint(p)}
              {#if isEveryone && state === "inherit"}<em>{inherited(p)}</em>{/if}
            </small>
          </span>
          <TriState
            value={state}
            label={label(p)}
            disabled={!roleEditable || !grantable(me, P[p])}
            onchange={(v) => set(P[p], v)}
          />
        </div>
      {/each}
    </div>
    {#if !isEveryone && roleEditable}
      <div><Button variant="plain" onclick={removeRole}>{t("chperm.removeRole")}</Button></div>
    {/if}
  </div>
</div>

<style>
  .pane {
    display: grid;
    grid-template-columns: 184px 1fr;
    gap: var(--sp-4);
    min-height: 220px;
  }
  .roles {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding-right: var(--sp-3);
    border-right: 1px solid var(--separator);
  }
  .role {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 30px;
    padding: 0 8px;
    border: none;
    border-radius: var(--r-sm);
    background: none;
    color: var(--text-1);
    font: inherit;
    text-align: left;
  }
  .role :global(svg) {
    flex: none;
    color: var(--text-3);
  }
  .role:hover {
    background: var(--bg-hover);
  }
  .role.sel {
    background: var(--bg-selected);
  }
  .role.sel :global(svg) {
    color: var(--accent-text);
  }
  .role.add {
    color: var(--text-2);
  }
  .nm {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
  }
  .empty {
    margin: 4px 8px;
    color: var(--text-3);
    font-size: var(--fs-small);
  }
  .detail {
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
    min-width: 0;
  }
  .intro {
    margin: 0;
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  .group {
    display: flex;
    flex-direction: column;
    border-radius: var(--r-lg);
    background: var(--bg-input);
  }
  .perm {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    min-height: 52px;
    padding: 8px 12px;
  }
  .perm + .perm {
    border-top: 1px solid var(--separator);
  }
  .two {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .two small {
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  em {
    font-style: normal;
    color: var(--text-3);
  }
</style>
