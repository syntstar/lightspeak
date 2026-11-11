<script lang="ts">
  import type { Member } from "../lib/api/types";
  import { app, api } from "../lib/state/app.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import Spinner from "../lib/ui/Spinner.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { userMenuItems } from "../lib/userMenu";
  import { memberAdminItems, memberLabel } from "../lib/spaceAdmin.svelte";

  const menuFor = (m: Member) => [...userMenuItems(m.userId), ...memberAdminItems(app.spaceId, m.userId)];

  const list = $derived(app.members[app.spaceId] ?? []);
  const groups = $derived.by(() => {
    const online: Member[] = [];
    const offline: Member[] = [];
    for (const m of list) {
      const st = app.status(m.userId);
      (st === "offline" || st === "invisible" ? offline : online).push(m);
    }
    const byName = (a: Member, b: Member) =>
      (app.user(a.userId)?.displayName ?? a.user.displayName).localeCompare(
        app.user(b.userId)?.displayName ?? b.user.displayName,
      );
    return { online: online.sort(byName), offline: offline.sort(byName) };
  });
</script>

{#snippet member(m: Member, dim: boolean)}
  {@const u = app.user(m.userId) ?? m.user}
  {@const st = app.status(m.userId)}
  <button
    type="button"
    class="m"
    class:dim
    onclick={(e) => openMenu(e, menuFor(m))}
    oncontextmenu={(e) => openMenu(e, menuFor(m))}
  >
    <Avatar
      name={u.displayName}
      id={m.userId}
      src={u.avatarUrl && api.url(u.avatarUrl)}
      size={32}
      status={dim ? undefined : st}
    />
    <span class="who">
      <span class="dn">
        {u.displayName}
        {#if memberLabel(m.spaceId, m.userId, m.roleIds ?? [], !!m.serverAdmin)}<span class="role"
            >{memberLabel(m.spaceId, m.userId, m.roleIds ?? [], !!m.serverAdmin)}</span
          >{/if}
      </span>
      {#if !dim && m.userId !== app.me?.id && app.presences[m.userId]?.statusText}
        <span class="st">{app.presences[m.userId]?.statusText}</span>
      {:else if !dim && m.userId === app.me?.id && app.me?.statusText}
        <span class="st">{app.me.statusText}</span>
      {/if}
    </span>
  </button>
{/snippet}

<aside class="members" aria-label={t("nav.members")}>
  {#if app.membersLoading[app.spaceId] && !list.length}
    <p class="loading"><Spinner size={14} /> {t("members.loading")}</p>
  {/if}
  {#if groups.online.length}
    <h3>{t("members.online", { count: groups.online.length })}</h3>
    {#each groups.online as m (m.userId)}{@render member(m, false)}{/each}
  {/if}
  {#if groups.offline.length}
    <h3>{t("members.offline", { count: groups.offline.length })}</h3>
    {#each groups.offline as m (m.userId)}{@render member(m, true)}{/each}
  {/if}
</aside>

<style>
  .members {
    width: var(--members-w);
    flex: none;
    overflow-y: auto;
    padding: 8px 8px 16px;
    background: var(--bg-content);
    border-left: 1px solid var(--separator);
  }
  h3 {
    margin: 14px 8px 4px;
    font-size: var(--fs-caption);
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-2);
  }
  .m {
    width: 100%;
    border: none;
    background: none;
    color: inherit;
    text-align: left;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 42px;
    padding: 0 8px;
    border-radius: var(--r-sm);
  }
  .m:hover {
    background: var(--bg-hover);
  }
  .dim {
    opacity: 0.45;
  }
  .who {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1;
    line-height: 1.25;
  }
  .dn,
  .st {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .dn {
    font-weight: 500;
  }
  .role {
    margin-left: 4px;
    font-size: var(--fs-caption);
    color: var(--text-3);
    font-weight: 500;
  }
  .st {
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .loading {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 16px 8px;
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
</style>
