<script lang="ts">
  // Channels of the current space grouped into sections, or the DM list.
  import Hash from "@lucide/svelte/icons/hash";
  import Volume2 from "@lucide/svelte/icons/volume-2";
  import Megaphone from "@lucide/svelte/icons/megaphone";
  import MicVocal from "@lucide/svelte/icons/mic-vocal";
  import Moon from "@lucide/svelte/icons/moon";
  import { channelIcon } from "../lib/channelIcons";
  import Lock from "@lucide/svelte/icons/lock";
  import EyeOff from "@lucide/svelte/icons/eye-off";
  import ChevronDown from "@lucide/svelte/icons/chevron-down";
  import MicOff from "@lucide/svelte/icons/mic-off";
  import HeadphoneOff from "@lucide/svelte/icons/headphone-off";
  import CheckCheck from "@lucide/svelte/icons/check-check";
  import Copy from "@lucide/svelte/icons/copy";
  import Plus from "@lucide/svelte/icons/plus";
  import UsersRound from "@lucide/svelte/icons/users-round";
  import LogOut from "@lucide/svelte/icons/log-out";
  import { userMenuItems } from "../lib/userMenu";
  import { errorCode } from "../lib/api/client";
  import { terr, tp } from "../lib/i18n/index.svelte";
  import type { MenuItem } from "../lib/ui/menu.svelte";
  import { notifyMenuItems } from "../lib/notifyMenu";
  import {
    applyOrder,
    can,
    channelAdminItems,
    channelRights,
    createItems,
    spaceAdmin,
  } from "../lib/spaceAdmin.svelte";
  import { reorder, type Move } from "../lib/channelOrder";
  import FolderPlus from "@lucide/svelte/icons/folder-plus";
  import { modeOf, restrictedForAll } from "../lib/channelRules";
  import { P } from "../lib/perms";
  import type { Channel } from "../lib/api/types";
  import { app, api, HOME } from "../lib/state/app.svelte";
  import { prefs, savePrefs } from "../lib/prefs.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { openMenu } from "../lib/ui/menu.svelte";
  import { toast } from "../lib/ui/toast.svelte";
  import Avatar from "../lib/ui/Avatar.svelte";
  import Badge from "../lib/ui/Badge.svelte";
  import { voice } from "../lib/voice/voice.svelte";

  // Categories: real, editable groups like Discord's; channels
  // without one stand on top without a heading. Who may manage channels
  // sorts by drag & drop (or the context menu).
  interface Section {
    key: string;
    title: string;
    cat: Channel;
    channels: Channel[];
  }

  const manage = $derived(app.spaceId !== HOME && can(app.spaceId, P.manageChannels));

  const layout = $derived.by((): { loose: Channel[]; sections: Section[] } => {
    if (app.spaceId === HOME) return { loose: [], sections: [] };
    const all = app.spaceChannels(app.spaceId);
    const cats = all.filter((c) => c.kind === "category");
    const ids = new Set(cats.map((c) => c.id));
    const loose = all.filter((c) => c.kind !== "category" && !(c.parentId && ids.has(c.parentId)));
    const sections: Section[] = [];
    for (const cat of cats) {
      const channels = all.filter((c) => c.kind !== "category" && c.parentId === cat.id);
      // A category whose channels are all private to others is hidden
      //; who can add channels keeps seeing it.
      if (!channels.length && !manage) continue;
      sections.push({ key: cat.id, title: cat.name, cat, channels });
    }
    return { loose, sections };
  });

  function sectionItems(s: Section): MenuItem[] {
    const collapsed = !!prefs.collapsed[s.key];
    const items: MenuItem[] = [
      { label: collapsed ? t("nav.expandGroup") : t("nav.collapseGroup"), action: () => toggle(s.key) },
    ];
    const create = createItems(app.spaceId, { parentId: s.cat.id, noInvite: true });
    if (manage)
      create.push({
        label: t("channel.createCategory"),
        icon: FolderPlus,
        action: () =>
          spaceAdmin.open({
            kind: "channel",
            spaceId: app.spaceId,
            channel: null,
            type: "category",
            parentId: null,
          }),
      });
    if (create.length) items.push({ separator: true }, ...create);
    items.push(...channelAdminItems(s.cat));
    return items;
  }

  function addTo(e: MouseEvent, s: Section) {
    openMenu(e, createItems(app.spaceId, { parentId: s.cat.id, noInvite: true }));
  }

  // ---------- drag & drop ----------
  let dragging = $state<{ id: string; cat: boolean } | null>(null);
  let dropAt = $state<{ id: string; where: "before" | "after" | "into" } | null>(null);

  function dragStart(e: DragEvent, c: Channel) {
    if (!manage || !e.dataTransfer) return;
    e.stopPropagation();
    dragging = { id: c.id, cat: c.kind === "category" };
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", c.name);
  }
  function dragEnd() {
    dragging = null;
    dropAt = null;
  }
  const half = (e: DragEvent): "before" | "after" => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return e.clientY < r.top + r.height / 2 ? "before" : "after";
  };
  function overRow(e: DragEvent, c: Channel) {
    if (!dragging || dragging.cat) return;
    e.preventDefault();
    e.stopPropagation();
    dropAt = dragging.id === c.id ? null : { id: c.id, where: half(e) };
  }
  function overHeader(e: DragEvent, cat: Channel) {
    if (!dragging) return;
    e.preventDefault();
    e.stopPropagation();
    if (dragging.cat) dropAt = dragging.id === cat.id ? null : { id: cat.id, where: half(e) };
    else dropAt = { id: cat.id, where: "into" };
  }
  function drop(e: DragEvent) {
    e.preventDefault();
    e.stopPropagation();
    const d = dragging;
    const at = dropAt;
    dragEnd();
    if (!d || !at) return;
    const m: Move = d.cat
      ? { id: d.id, category: at.id, after: at.where === "after" }
      : at.where === "into"
        ? { id: d.id, into: at.id }
        : at.where === "after"
          ? { id: d.id, before: at.id, after: true }
          : { id: d.id, before: at.id };
    void applyOrder(app.spaceId, reorder(app.spaceChannels(app.spaceId), m));
  }
  const dropClass = (id: string) => (dropAt?.id === id ? `drop-${dropAt.where}` : "");

  /** Right-click on the free area of the channel list. */
  function listMenu(e: MouseEvent) {
    if (app.spaceId === HOME) return;
    const items = createItems(app.spaceId);
    if (items.length) openMenu(e, items);
  }

  function toggle(key: string) {
    if (prefs.collapsed[key]) delete prefs.collapsed[key];
    else prefs.collapsed[key] = true;
    savePrefs();
  }

  function channelMenu(e: MouseEvent, c: Channel) {
    openMenu(e, [
      {
        label: t("channel.markRead"),
        icon: CheckCheck,
        disabled: !app.isUnread(c),
        action: () => {
          if (c.lastMessageId)
            void api.post(`/channels/${c.id}/ack`, { messageId: c.lastMessageId }).catch(() => {});
        },
      },
      { separator: true },
      ...(c.kind === "text"
        ? [...notifyMenuItems({ kind: "channel", id: c.id }), { separator: true as const }]
        : []),
      {
        label: t("channel.copyId"),
        icon: Copy,
        action: () =>
          void navigator.clipboard.writeText(c.id).then(() => toast(t("toast.copied"), "success")),
      },
      ...channelAdminItems(c),
    ]);
  }

  let { onNewDM }: { onNewDM: () => void } = $props();

  function dmMenu(e: MouseEvent, c: Channel) {
    const peer = app.dmPeer(c);
    const items: MenuItem[] = [
      {
        label: t("channel.markRead"),
        icon: CheckCheck,
        disabled: !app.isUnread(c),
        action: () => {
          if (c.lastMessageId)
            void api.post(`/channels/${c.id}/ack`, { messageId: c.lastMessageId }).catch(() => {});
        },
      },
      { separator: true },
      ...notifyMenuItems({ kind: "channel", id: c.id }),
      { separator: true },
    ];
    if (peer) items.push(...userMenuItems(peer.id));
    else
      items.push({
        label: t("dm.leaveGroup"),
        icon: LogOut,
        danger: true,
        action: () => {
          if (confirm(t("dm.leaveConfirm", { name: app.channelTitle(c) })))
            app.leaveDM(c.id).catch((err: unknown) => toast(terr(errorCode(err)), "error"));
        },
      });
    openMenu(e, items);
  }
  /** What kind of channel this is for most people: read-only
   *  text channels and voice channels where only some speak get their own
   *  icon, like the away channel. An icon picked in the channel settings
   *  wins, except for the away channel's moon. */
  function look(c: Channel): "away" | "custom" | "readonly" | "stage" | "voice" | "text" {
    if (c.kind === "voice" && modeOf(c) === "away") return "away";
    if (c.icon) return "custom";
    if (c.kind === "voice") {
      return restrictedForAll(c, P.speak) ? "stage" : "voice";
    }
    return restrictedForAll(c, P.send) ? "readonly" : "text";
  }
  /** Tooltip saying what the current user cannot do here. */
  function ruleTip(c: Channel): string | undefined {
    if (!c.spaceId) return undefined;
    const r = channelRights(c);
    const parts: string[] = [];
    if (restrictedForAll(c, P.view)) parts.push(t("rules.tipPrivate"));
    if (modeOf(c) === "away") parts.push(t("rules.tipAway"));
    if (c.kind === "voice") {
      if (!r.connect) parts.push(t("rules.tipNoConnect"));
      else if (!r.speak && modeOf(c) !== "away") parts.push(t("rules.tipListen"));
    } else if (!r.send) parts.push(t("rules.tipReadOnly"));
    return parts.length ? parts.join(" · ") : undefined;
  }

  function joinVoice(c: Channel) {
    if (!channelRights(c).connect) {
      toast(terr("channel_locked"), "info");
      return;
    }
    void voice.join(c.id);
  }
</script>

{#snippet row(c: Channel)}
  {@const unread = app.isUnread(c)}
  {@const mentions = app.mentions(c)}
  {@const kind = look(c)}
  <button
    type="button"
    class="ch {dropClass(c.id)}"
    class:dragged={dragging?.id === c.id}
    draggable={manage}
    ondragstart={(e) => dragStart(e, c)}
    ondragend={dragEnd}
    ondragover={(e) => overRow(e, c)}
    ondrop={drop}
    class:selected={app.channelId === c.id}
    class:connected={c.kind === "voice" && voice.channelId === c.id}
    class:unread
    class:muted={app.muted(c)}
    aria-current={app.channelId === c.id ? "page" : undefined}
    title={ruleTip(c)}
    aria-label={c.kind === "voice"
      ? voice.channelId === c.id
        ? `${c.name} – ${t("voice.connected")}`
        : `${c.name} – ${t("voice.join")}`
      : undefined}
    onclick={() =>
      c.kind !== "voice"
        ? (voice.stream.close(), app.selectChannel(c.id))
        : voice.channelId === c.id && voice.stream.watching.length
          ? voice.stream.open()
          : joinVoice(c)}
    oncontextmenu={(e) => channelMenu(e, c)}
  >
    {#if kind === "custom"}{@const Icon = channelIcon(c.kind === "voice" ? "voice" : "text", c.icon)}<Icon
        size={16}
        strokeWidth={1.75}
      />{:else if kind === "readonly"}<Megaphone
        size={16}
        strokeWidth={1.75}
      />{:else if kind === "stage"}<MicVocal size={16} strokeWidth={1.75} />{:else if kind === "away"}<Moon
        size={16}
        strokeWidth={1.75}
      />{:else if c.kind === "voice"}<Volume2 size={16} strokeWidth={1.75} />{:else}<Hash
        size={16}
        strokeWidth={1.75}
      />{/if}
    <span class="name">{c.name}</span>
    {#if c.spaceId && modeOf(c) !== "away" && restrictedForAll(c, P.view)}<span
        class="lock"
        aria-hidden="true"><EyeOff size={12} strokeWidth={2} /></span
      >{/if}
    {#if c.kind === "voice" && c.spaceId && !channelRights(c).connect}<span class="lock"
        ><Lock size={12} strokeWidth={2} /></span
      >{/if}
    <Badge count={mentions} />
  </button>
  {#if c.kind === "voice"}
    {#each app.voiceMembers(c.id) as v (v.userId)}
      {@const u = app.user(v.userId)}
      <div class="vu">
        <button
          type="button"
          class="voice-user"
          class:speaking={!!voice.speaking[v.userId]}
          class:lmuted={voice.isLocallyMuted(v.userId)}
          onclick={(e) => openMenu(e, userMenuItems(v.userId))}
          oncontextmenu={(e) => openMenu(e, userMenuItems(v.userId))}
        >
          <Avatar
            name={u?.displayName ?? "?"}
            id={v.userId}
            src={u?.avatarUrl && api.url(u.avatarUrl)}
            size={20}
            speaking={!!voice.speaking[v.userId] && !v.muted && !v.serverMuted}
          />
          <span class="name">{u?.displayName ?? t("common.unknownUser")}</span>
          {#if v.deafened}<HeadphoneOff
              size={14}
              strokeWidth={1.75}
            />{:else if v.muted || v.serverMuted}<MicOff
              size={14}
              strokeWidth={1.75}
              class={v.serverMuted ? "srv" : ""}
            />{/if}
        </button>
        {#if v.streaming}
          <button
            type="button"
            class="live"
            class:on={voice.stream.isWatching(v.userId)}
            aria-label={t("stream.watchUser", { name: u?.displayName ?? t("common.unknownUser") })}
            title={t("stream.watch")}
            onclick={() =>
              voice.stream.isWatching(v.userId)
                ? voice.stream.open(v.userId)
                : void voice.stream.watch(v.userId)}>LIVE</button
          >
        {/if}
      </div>
    {/each}
  {/if}
{/snippet}

<div class="list" oncontextmenu={listMenu} role="presentation">
  {#if app.spaceId === HOME}
    {@const pending = Object.values(app.relationships).filter((r) => r.kind === "pending_in").length}
    <button
      type="button"
      class="ch dm friends"
      class:selected={app.channelId === null}
      aria-current={app.channelId === null ? "page" : undefined}
      onclick={() => app.showFriends()}
    >
      <span class="fr-ic"><UsersRound size={18} strokeWidth={1.75} /></span>
      <span class="name">{t("nav.friends")}</span>
      <Badge count={pending} />
    </button>
    <div class="sec-row">
      <span class="sec-title static">{t("nav.dms")}</span>
      <button type="button" class="add" aria-label={t("dm.new")} title={t("dm.new")} onclick={onNewDM}>
        <Plus size={14} strokeWidth={2} />
      </button>
    </div>
    {#each app.dms() as c (c.id)}
      {@const peer = app.dmPeer(c)}
      {@const unread = app.isUnread(c)}
      <button
        type="button"
        class="ch dm"
        class:selected={app.channelId === c.id}
        class:unread
        class:muted={app.muted(c)}
        aria-current={app.channelId === c.id ? "page" : undefined}
        onclick={() => app.selectChannel(c.id)}
        oncontextmenu={(e) => dmMenu(e, c)}
      >
        <Avatar
          name={app.channelTitle(c)}
          id={peer?.id ?? c.id}
          src={peer?.avatarUrl && api.url(peer.avatarUrl)}
          size={28}
          status={peer ? app.status(peer.id) : undefined}
        />
        <span class="name"
          >{app.channelTitle(c)}{#if c.kind === "group_dm"}<span class="sub"
              >{tp("dm.members", c.memberIds?.length ?? 0)}</span
            >{/if}</span
        >
        <Badge count={app.mentions(c)} />
      </button>
    {:else}
      <p class="empty">{t("nav.noDms")}</p>
    {/each}
  {:else}
    {#each layout.loose as c (c.id)}
      {@render row(c)}
    {/each}
    {#each layout.sections as s (s.key)}
      {@const collapsed = !!prefs.collapsed[s.key]}
      <div
        class="sec-row group {dropClass(s.cat.id)}"
        class:dragged={dragging?.id === s.cat.id}
        draggable={manage}
        ondragstart={(e) => dragStart(e, s.cat)}
        ondragend={dragEnd}
        ondragover={(e) => overHeader(e, s.cat)}
        ondrop={drop}
        oncontextmenu={(e) => openMenu(e, sectionItems(s))}
        role="presentation"
      >
        <button
          type="button"
          class="sec-title"
          aria-expanded={!collapsed}
          aria-label={`${s.title} – ${collapsed ? t("nav.expand") : t("nav.collapse")}`}
          onclick={() => toggle(s.key)}
        >
          <span class="chev" class:collapsed><ChevronDown size={12} strokeWidth={2.25} /></span>
          {s.title}
        </button>
        {#if manage}
          {@const label = t("channel.createIn", { name: s.title })}
          <button
            type="button"
            class="add hover"
            aria-label={label}
            title={label}
            onclick={(e) => addTo(e, s)}
          >
            <Plus size={14} strokeWidth={2} />
          </button>
        {/if}
      </div>
      {#if !s.channels.length && !collapsed}
        <p class="empty small">{t("nav.emptyGroup")}</p>
      {/if}
      {#each s.channels as c (c.id)}
        {#if !collapsed || app.channelId === c.id || app.voiceMembers(c.id).length}
          {@render row(c)}
        {/if}
      {/each}
    {/each}
    {#if !layout.loose.length && !layout.sections.length}
      <p class="empty">{t("nav.noChannels")}</p>
    {/if}
  {/if}
</div>

<style>
  .list {
    flex: 1;
    overflow-y: auto;
    padding: 4px 8px 12px;
  }
  .sec-title {
    display: flex;
    align-items: center;
    gap: 2px;
    width: 100%;
    margin-top: 14px;
    padding: 2px 4px;
    border: none;
    background: none;
    font-size: var(--fs-caption);
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-2);
    text-align: left;
  }
  .sec-title:hover {
    color: var(--text-1);
  }
  .sec-title.static {
    padding-left: 8px;
  }
  .sec-row {
    display: flex;
    align-items: center;
    margin-top: 14px;
  }
  .sec-row .sec-title {
    margin-top: 0;
  }
  .sec-row.group .sec-title {
    flex: 1;
    min-width: 0;
  }
  /* The "+" of a channel group shows on hover and keyboard focus. */
  .add.hover {
    opacity: 0;
  }
  .sec-row:hover .add.hover,
  .add.hover:focus-visible {
    opacity: 1;
  }
  .add {
    display: inline-flex;
    padding: 3px;
    margin-right: 4px;
    border: none;
    border-radius: var(--r-xs);
    background: none;
    color: var(--text-2);
  }
  .add:hover {
    color: var(--text-1);
    background: var(--bg-hover);
  }
  .friends {
    margin-top: 6px;
  }
  .fr-ic {
    width: 28px;
    height: 28px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
  }
  .sub {
    display: block;
    font-size: var(--fs-caption);
    font-weight: 400;
    color: var(--text-3);
  }
  .chev {
    display: inline-flex;
    transition: transform var(--dur-fast) var(--ease-out);
  }
  .chev.collapsed {
    transform: rotate(-90deg);
  }
  .ch {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    height: 30px;
    margin-top: 1px;
    padding: 0 8px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    text-align: left;
  }
  .ch.dm {
    height: 40px;
    gap: 10px;
  }
  .ch:hover {
    background: var(--bg-hover);
    color: var(--text-1);
  }
  .ch.selected {
    background: var(--bg-selected);
    color: var(--text-1);
  }
  .ch.muted:not(.selected) {
    opacity: 0.5;
  }
  .ch.unread {
    color: var(--text-1);
    font-weight: 600;
  }
  .ch :global(svg) {
    flex: none;
    opacity: 0.8;
  }
  .lock {
    display: inline-flex;
    flex: none;
    color: var(--text-3);
  }
  .name {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .voice-user {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    height: 28px;
    padding: 0 8px 0 30px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    font-size: var(--fs-callout);
    text-align: left;
  }
  .voice-user:hover {
    background: var(--bg-hover);
    color: var(--text-1);
  }
  .voice-user.speaking {
    color: var(--text-1);
  }
  .voice-user.lmuted .name {
    text-decoration: line-through;
    opacity: 0.7;
  }
  .voice-user :global(svg) {
    color: var(--text-3);
    flex: none;
  }
  .voice-user :global(svg.srv) {
    color: var(--danger);
  }
  .ch.connected {
    color: var(--success-text);
  }
  .vu {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .vu .voice-user {
    flex: 1;
    min-width: 0;
  }
  .live {
    flex: none;
    margin-right: 6px;
    padding: 1px 6px;
    border: none;
    border-radius: var(--r-xs);
    background: var(--live);
    color: #fff;
    font-size: 9.5px;
    font-weight: 700;
    letter-spacing: 0.04em;
    line-height: 14px;
  }
  .live:hover {
    filter: brightness(1.12);
  }
  .live.on {
    box-shadow: 0 0 0 2px var(--accent);
  }
  /* Drag & drop: a line where the item lands, a frame around the category
     that takes the channel. */
  .drop-before {
    box-shadow: inset 0 2px 0 var(--accent);
  }
  .drop-after {
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  .sec-row.drop-into {
    border-radius: var(--r-sm);
    box-shadow: inset 0 0 0 2px var(--accent);
  }
  .dragged {
    opacity: 0.4;
  }
  .empty {
    margin: 16px 8px;
    color: var(--text-3);
    font-size: var(--fs-callout);
  }
  .empty.small {
    margin: 2px 8px 4px 20px;
    font-size: var(--fs-small);
  }
</style>
