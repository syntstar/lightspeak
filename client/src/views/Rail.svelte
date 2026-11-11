<script lang="ts">
  import MessageCircle from "@lucide/svelte/icons/message-circle";
  import Plus from "@lucide/svelte/icons/plus";
  import Link from "@lucide/svelte/icons/link";
  import CheckCheck from "@lucide/svelte/icons/check-check";
  import Copy from "@lucide/svelte/icons/copy";
  import { app, api, HOME } from "../lib/state/app.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { tooltip } from "../lib/ui/tooltip";
  import { toast } from "../lib/ui/toast.svelte";
  import Badge from "../lib/ui/Badge.svelte";
  import { notifyMenuItems } from "../lib/notifyMenu";
  import { spaceAdminItems } from "../lib/spaceAdmin.svelte";

  let { onCreate, onJoin }: { onCreate: () => void; onJoin: () => void } = $props();

  function initials(name: string): string {
    return (
      name
        .trim()
        .split(/\s+/)
        .slice(0, 2)
        .map((p) => [...p][0] ?? "")
        .join("")
        .toUpperCase() || "?"
    );
  }

  async function copy(text: string) {
    await navigator.clipboard.writeText(text);
    toast(t("toast.copied"), "success");
  }

  function markRead(spaceId: string) {
    for (const c of app.spaceChannels(spaceId)) {
      if (app.isUnread(c) && c.lastMessageId) {
        void api.post(`/channels/${c.id}/ack`, { messageId: c.lastMessageId }).catch(() => {});
      }
    }
  }

  function spaceMenu(e: MouseEvent, id: string) {
    openMenu(e, [
      { label: t("space.markRead"), icon: CheckCheck, action: () => markRead(id) },
      { separator: true },
      ...notifyMenuItems({ kind: "space", id }),
      { separator: true },
      { label: t("space.copyId"), icon: Copy, action: () => void copy(id) },
      ...(spaceAdminItems(id).length ? [{ separator: true as const }, ...spaceAdminItems(id)] : []),
    ]);
  }

  const home = $derived(app.spaceUnread(HOME));
</script>

<nav class="rail" aria-label={t("nav.dms")}>
  <div class="drag" data-tauri-drag-region></div>
  <div class="item" class:selected={app.spaceId === HOME}>
    <span class="pill" class:unread={home.unread}></span>
    <button
      type="button"
      class="sq home"
      aria-label={t("nav.home")}
      aria-current={app.spaceId === HOME ? "page" : undefined}
      use:tooltip={{ text: t("nav.home"), placement: "right" }}
      onclick={() => app.selectSpace(HOME)}
    >
      <MessageCircle size={20} strokeWidth={1.75} />
    </button>
    {#if home.mentions > 0}<span class="badge"><Badge count={home.mentions} /></span>{/if}
  </div>
  <div class="sep"></div>
  <div class="list">
    {#each app.spaceOrder as id (id)}
      {@const sp = app.spaces[id]}
      {#if sp}
        {@const u = app.spaceUnread(id)}
        <div class="item" class:selected={app.spaceId === id}>
          <span class="pill" class:unread={u.unread}></span>
          <button
            type="button"
            class="sq"
            aria-label={sp.name}
            aria-current={app.spaceId === id ? "page" : undefined}
            use:tooltip={{ text: sp.name, placement: "right" }}
            onclick={() => app.selectSpace(id)}
            oncontextmenu={(e) => spaceMenu(e, id)}
          >
            {#if sp.iconUrl}
              <img src={api.url(sp.iconUrl)} alt="" width="40" height="40" loading="lazy" decoding="async" />
            {:else}
              <span class="ini">{initials(sp.name)}</span>
            {/if}
          </button>
          {#if u.mentions > 0}<span class="badge"><Badge count={u.mentions} /></span>{/if}
        </div>
      {/if}
    {/each}
    <!-- Guests stay in the space they were invited to. -->
    {#if !app.me?.guest}
      <div class="item">
        <button
          type="button"
          class="sq add"
          aria-label={t("nav.addSpace")}
          use:tooltip={{ text: t("nav.addSpace"), placement: "right" }}
          onclick={(e) =>
            openMenu(e, [
              { label: t("space.create"), icon: Plus, action: onCreate },
              { label: t("nav.joinSpace"), icon: Link, action: onJoin },
            ])}
        >
          <Plus size={20} strokeWidth={1.75} />
        </button>
      </div>
    {/if}
  </div>
</nav>

<style>
  .rail {
    width: var(--rail-w);
    flex: none;
    display: flex;
    flex-direction: column;
    align-items: center;
    background: var(--bg-rail);
    border-right: 1px solid var(--separator);
  }
  .drag {
    height: 12px;
    width: 100%;
    flex: none;
  }
  .list {
    flex: 1;
    width: 100%;
    overflow-y: auto;
    overflow-x: hidden;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding-bottom: 12px;
    scrollbar-width: none;
  }
  .item {
    position: relative;
    width: 100%;
    display: flex;
    justify-content: center;
    margin-top: 0;
  }
  .sq {
    width: 40px;
    height: 40px;
    border-radius: var(--r-squircle);
    border: none;
    padding: 0;
    overflow: hidden;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--bg-active);
    color: var(--text-1);
    transition:
      border-radius var(--dur-base) var(--ease-out),
      background-color var(--dur-fast);
  }
  .sq:hover {
    background: color-mix(in srgb, var(--text-1) 16%, transparent);
  }
  .selected .sq {
    background: var(--accent);
    color: var(--on-accent);
  }
  .sq img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
  .ini {
    font-size: 14px;
    font-weight: 600;
  }
  .add {
    color: var(--success-text);
    background: transparent;
    border: 1px dashed var(--border-hairline);
  }
  .add:hover {
    background: var(--bg-hover);
  }
  .pill {
    position: absolute;
    left: 0;
    top: 50%;
    width: 4px;
    height: 0;
    border-radius: 0 4px 4px 0;
    background: var(--text-1);
    transform: translateY(-50%);
    transition: height var(--dur-base) var(--ease-spring);
  }
  .pill.unread {
    height: 8px;
  }
  .item:hover .pill {
    height: 16px;
  }
  .selected .pill {
    height: 28px;
  }
  .badge {
    position: absolute;
    right: 6px;
    bottom: -3px;
    pointer-events: none;
    border-radius: var(--r-full);
    box-shadow: 0 0 0 3px var(--bg-sidebar-solid);
  }
  .sep {
    width: 28px;
    height: 1px;
    margin: 8px 0;
    background: var(--separator);
    flex: none;
  }
</style>
