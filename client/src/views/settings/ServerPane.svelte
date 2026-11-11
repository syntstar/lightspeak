<script lang="ts">
  // Settings → Server (server admins only): every space on this
  // server, also those the admin is not a member of. Contents stay end-to-end
  // encrypted – joining is a normal, visible membership.
  import { onMount } from "svelte";
  import type { ID, User } from "../../lib/api/types";
  import { errorCode } from "../../lib/api/client";
  import { app, api } from "../../lib/state/app.svelte";
  import { t, tp, terr } from "../../lib/i18n/index.svelte";
  import Avatar from "../../lib/ui/Avatar.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import Spinner from "../../lib/ui/Spinner.svelte";
  import TextField from "../../lib/ui/TextField.svelte";
  import { toast } from "../../lib/ui/toast.svelte";

  interface AdminSpace {
    id: ID;
    name: string;
    iconUrl?: string;
    ownerId: ID;
    owner?: User;
    memberCount: number;
    member: boolean;
  }

  let spaces = $state<AdminSpace[] | null>(null);
  let busy = $state<ID | null>(null);
  let deleting = $state<ID | null>(null);
  let confirmName = $state("");

  async function load() {
    try {
      spaces = await api.get<AdminSpace[]>("/admin/spaces");
    } catch (err) {
      spaces = [];
      toast(terr(errorCode(err)), "error");
    }
  }
  onMount(load);

  async function run(id: ID, fn: () => Promise<void>) {
    busy = id;
    try {
      await fn();
      await load();
    } catch (err) {
      toast(terr(errorCode(err)), "error");
    } finally {
      busy = null;
    }
  }

  const join = (sp: AdminSpace) =>
    run(sp.id, async () => {
      // The space itself arrives over the gateway (space_create).
      await api.post(`/admin/spaces/${sp.id}/join`, {});
      toast(t("server.joined", { name: sp.name }), "success");
    });

  function open(sp: AdminSpace) {
    app.settingsOpen = false;
    app.selectSpace(sp.id);
  }

  function takeOver(sp: AdminSpace) {
    if (!confirm(t("server.takeOverConfirm", { name: sp.name, owner: sp.owner?.displayName ?? "?" }))) return;
    void run(sp.id, async () => {
      if (!sp.member) await api.post(`/admin/spaces/${sp.id}/join`, {});
      await api.post(`/admin/spaces/${sp.id}/owner`, { userId: app.me!.id });
    });
  }

  function askDelete(sp: AdminSpace) {
    deleting = deleting === sp.id ? null : sp.id;
    confirmName = "";
  }

  function remove(sp: AdminSpace, e: SubmitEvent) {
    e.preventDefault();
    void run(sp.id, async () => {
      await api.request(`/admin/spaces/${sp.id}`, { method: "DELETE", body: { confirmName } });
      deleting = null;
    });
  }
</script>

<p class="note">{t("server.explain")}</p>
<p class="gh">{t("server.spaces")}</p>
<div class="group">
  {#if spaces === null}
    <div class="row"><Spinner size={14} /></div>
  {:else}
    {#each spaces as sp (sp.id)}
      <div class="row">
        <span class="who">
          <Avatar name={sp.name} id={sp.id} src={sp.iconUrl && api.url(sp.iconUrl)} size={28} />
          <span class="two">
            {sp.name}
            <small
              >{t("server.ownerLine", {
                owner: sp.ownerId === app.me?.id ? t("members.you") : (sp.owner?.displayName ?? "?"),
                members: tp("server.members", sp.memberCount),
              })}</small
            >
          </span>
        </span>
        <span class="actions">
          {#if sp.member}
            <Button variant="plain" onclick={() => open(sp)}>{t("server.open")}</Button>
          {:else}
            <Button variant="plain" busy={busy === sp.id} onclick={() => void join(sp)}
              >{t("server.join")}</Button
            >
          {/if}
          {#if sp.ownerId !== app.me?.id}
            <Button variant="plain" busy={busy === sp.id} onclick={() => takeOver(sp)}
              >{t("server.takeOver")}</Button
            >
          {/if}
          <Button variant="plain" aria-expanded={deleting === sp.id} onclick={() => askDelete(sp)}
            ><span class="danger">{t("server.delete")}</span></Button
          >
        </span>
      </div>
      {#if deleting === sp.id}
        <form class="row del" onsubmit={(e) => remove(sp, e)}>
          <small>{t("spaceSettings.deleteHint", { name: sp.name })}</small>
          <span class="del-row">
            <TextField
              label={t("spaceSettings.deleteName")}
              bind:value={confirmName}
              maxlength={64}
              autofocus
            />
            <Button variant="danger" type="submit" busy={busy === sp.id} disabled={confirmName !== sp.name}
              >{t("spaceSettings.delete")}</Button
            >
          </span>
        </form>
      {/if}
    {:else}
      <div class="row"><small>{t("server.none")}</small></div>
    {/each}
  {/if}
</div>

<style>
  .who {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .two {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .two small {
    color: var(--text-2);
    font-size: var(--fs-small);
  }
  .actions {
    display: inline-flex;
    gap: 2px;
    flex: none;
  }
  .danger {
    color: var(--danger);
  }
  .del {
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
  }
  .del small {
    color: var(--text-2);
  }
  .del-row {
    display: flex;
    gap: 8px;
    align-items: flex-end;
  }
  .del-row :global(label) {
    flex: 1;
  }
</style>
