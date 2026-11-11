<script lang="ts">
  // Space settings → Roles: list of roles (highest first, @everyone
  // last) and an editor for the selected one. Permission switches save
  // immediately; what the current user may not change is disabled, the
  // server checks it again.
  import ArrowUp from "@lucide/svelte/icons/arrow-up";
  import ArrowDown from "@lucide/svelte/icons/arrow-down";
  import Plus from "@lucide/svelte/icons/plus";
  import ChevronRight from "@lucide/svelte/icons/chevron-right";
  import type { ID } from "../lib/api/types";
  import { errorCode } from "../lib/api/client";
  import { app, api } from "../lib/state/app.svelte";
  import { t, tp, terr, type MessageKey } from "../lib/i18n/index.svelte";
  import { P, PERM_GROUPS, canManageRole, grantable, has } from "../lib/perms";
  import { accessOf } from "../lib/spaceAdmin.svelte";
  import { toast } from "../lib/ui/toast.svelte";
  import Button from "../lib/ui/Button.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";
  import TextField from "../lib/ui/TextField.svelte";
  import Toggle from "../lib/ui/Toggle.svelte";

  let { spaceId }: { spaceId: ID } = $props();

  const space = $derived(app.spaces[spaceId]);
  const me = $derived(accessOf(spaceId));
  const roles = $derived(space?.roles ?? []);
  let sel = $state<ID | null>(null);
  let name = $state("");
  let busy = $state(false);

  /** Selected role, or @everyone (id = space id, position 0). */
  const role = $derived.by(() => {
    if (sel === spaceId)
      return { id: spaceId, name: "@everyone", position: 0, permissions: space?.everyonePerms ?? 0 };
    return roles.find((r) => r.id === sel) ?? null;
  });
  const editable = $derived(!!role && canManageRole(me, role.position, 0));
  const isEveryone = $derived(sel === spaceId);

  // Keep the name field in step with the selection and remote renames, but
  // not with unrelated updates (e.g. a permission switch) while typing.
  let nameFor = "";
  $effect(() => {
    const key = `${sel}|${role?.name ?? ""}`;
    if (key === nameFor) return;
    nameFor = key;
    name = role && !isEveryone ? role.name : "";
  });

  const count = (id: ID) => (app.members[spaceId] ?? []).filter((m) => m.roleIds?.includes(id)).length;

  function fail(err: unknown) {
    toast(terr(errorCode(err)), "error");
  }

  async function create() {
    busy = true;
    try {
      const r = await api.post<{ id: ID }>(`/spaces/${spaceId}/roles`, {
        name: t("roles.newName"),
        permissions: 0,
      });
      sel = r.id;
    } catch (err) {
      fail(err);
    } finally {
      busy = false;
    }
  }

  // Switches show the new state at once; saves run one after another so a
  // quick second click never works from a stale value.
  let pending = $state<Record<ID, number>>({});
  const permsOf = (r: { id: ID; permissions: number }) => pending[r.id] ?? r.permissions;
  let queue: Promise<unknown> = Promise.resolve();

  function setPerm(bit: number, on: boolean) {
    if (!role) return;
    const id = role.id;
    const permissions = on ? permsOf(role) | bit : permsOf(role) & ~bit;
    pending[id] = permissions;
    queue = queue
      .then(() => api.patch(`/spaces/${spaceId}/roles/${id}`, { permissions }))
      .catch(fail)
      .finally(() => {
        if (pending[id] === permissions) delete pending[id];
      });
  }

  async function rename(e: SubmitEvent) {
    e.preventDefault();
    if (!role || isEveryone || !name.trim() || name.trim() === role.name) return;
    try {
      await api.patch(`/spaces/${spaceId}/roles/${role.id}`, { name: name.trim() });
      toast(t("spaceSettings.saved"), "success");
    } catch (err) {
      fail(err);
    }
  }

  async function move(id: ID, dir: 1 | -1) {
    try {
      await api.post(`/spaces/${spaceId}/roles/${id}/move`, { dir });
    } catch (err) {
      fail(err);
    }
  }

  async function remove() {
    if (!role || isEveryone) return;
    if (!confirm(t("roles.deleteConfirm", { name: role.name }))) return;
    try {
      await api.del(`/spaces/${spaceId}/roles/${role.id}`);
      sel = null;
    } catch (err) {
      fail(err);
    }
  }

  const permLabel = (p: string) => t(`perm.${p}` as MessageKey);
  const permHint = (p: string) => t(`perm.${p}.hint` as MessageKey);
</script>

{#if !role}
  <p class="hint">{t("roles.explain")}</p>
  <div class="list" role="list">
    {#each roles as r, i (r.id)}
      {@const manageable = canManageRole(me, r.position, 0)}
      <div class="item" role="listitem">
        <button type="button" class="pick" onclick={() => (sel = r.id)}>
          <span class="two">
            {r.name}
            <small>{tp("roles.members", count(r.id))}</small>
          </span>
          <ChevronRight size={15} strokeWidth={1.75} />
        </button>
        {#if manageable}
          <IconButton
            label={t("roles.moveUp", { name: r.name })}
            disabled={i === 0 || !canManageRole(me, roles[i - 1]!.position, 0)}
            onclick={() => void move(r.id, 1)}><ArrowUp size={15} strokeWidth={1.75} /></IconButton
          >
          <IconButton
            label={t("roles.moveDown", { name: r.name })}
            disabled={i === roles.length - 1}
            onclick={() => void move(r.id, -1)}><ArrowDown size={15} strokeWidth={1.75} /></IconButton
          >
        {/if}
      </div>
    {/each}
    <div class="item" role="listitem">
      <button type="button" class="pick" onclick={() => (sel = spaceId)}>
        <span class="two">
          @everyone
          <small>{t("roles.everyoneHint")}</small>
        </span>
        <ChevronRight size={15} strokeWidth={1.75} />
      </button>
    </div>
  </div>
  {#if has(me, P.manageRoles)}
    <div>
      <Button {busy} onclick={() => void create()}
        ><Plus size={14} strokeWidth={2} />{t("roles.create")}</Button
      >
    </div>
  {/if}
{:else}
  <div class="head">
    <Button variant="plain" onclick={() => (sel = null)}>{t("roles.back")}</Button>
  </div>
  {#if !isEveryone}
    <form class="row-form" onsubmit={(e) => void rename(e)}>
      <TextField label={t("roles.name")} bind:value={name} maxlength={32} disabled={!editable} />
      <Button type="submit" disabled={!editable || !name.trim() || name.trim() === role.name}
        >{t("common.save")}</Button
      >
    </form>
  {:else}
    <p class="hint">{t("roles.everyoneExplain")}</p>
  {/if}
  {#if !editable}<p class="hint">{t("roles.readOnly")}</p>{/if}
  {#each PERM_GROUPS as g (g.key)}
    <h3 class="gh">{t(`perm.group.${g.key}` as MessageKey)}</h3>
    <div class="perms">
      {#each g.perms as p (p)}
        {@const bit = P[p]}
        <div class="perm">
          <span class="two">
            {permLabel(p)}
            <small>{permHint(p)}</small>
          </span>
          <Toggle
            label={permLabel(p)}
            checked={(permsOf(role) & bit) === bit}
            disabled={!editable || !grantable(me, bit)}
            onchange={(v) => setPerm(bit, v)}
          />
        </div>
      {/each}
    </div>
  {/each}
  {#if !isEveryone}
    <h3 class="gh">{t("perm.group.admin")}</h3>
    <div class="perms">
      <div class="perm">
        <span class="two">
          {permLabel("administrator")}
          <small>{permHint("administrator")}</small>
        </span>
        <Toggle
          label={permLabel("administrator")}
          checked={(permsOf(role) & P.administrator) !== 0}
          disabled={!editable || !grantable(me, P.administrator)}
          onchange={(v) => setPerm(P.administrator, v)}
        />
      </div>
    </div>
    {#if editable}
      <div><Button variant="danger" onclick={() => void remove()}>{t("roles.delete")}</Button></div>
    {/if}
  {/if}
{/if}

<style>
  .hint {
    margin: 0;
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  .list,
  .perms {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .item,
  .perm {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 40px;
    padding: 4px 6px;
    border-radius: var(--r-sm);
  }
  .item:hover {
    background: var(--bg-hover);
  }
  .pick {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0;
    border: none;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
  }
  .pick :global(svg) {
    color: var(--text-3);
  }
  .two {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .two small {
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  .gh {
    margin: 8px 0 -6px;
    font-size: var(--fs-small);
    font-weight: 600;
    color: var(--text-2);
  }
  .head {
    margin: -6px 0 -4px -8px;
  }
  .row-form {
    display: flex;
    gap: 8px;
    align-items: flex-end;
  }
  .row-form :global(label) {
    flex: 1;
  }
</style>
