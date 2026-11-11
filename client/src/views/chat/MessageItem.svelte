<script lang="ts">
  import Reply from "@lucide/svelte/icons/reply";
  import Pencil from "@lucide/svelte/icons/pencil";
  import Trash from "@lucide/svelte/icons/trash-2";
  import Copy from "@lucide/svelte/icons/copy";
  import Pin from "@lucide/svelte/icons/pin";
  import SmilePlus from "@lucide/svelte/icons/smile-plus";
  import FileIcon from "@lucide/svelte/icons/file";
  import Download from "@lucide/svelte/icons/download";
  import CornerUpRight from "@lucide/svelte/icons/corner-up-right";
  import type { Attachment } from "../../lib/api/types";
  import { app, api, displayUser } from "../../lib/state/app.svelte";
  import { messages, type ChatMessage } from "../../lib/state/messages.svelte";
  import { userMenuItems } from "../../lib/userMenu";
  import { channelRights } from "../../lib/spaceAdmin.svelte";
  import { parse, toPlain } from "../../lib/markdown";
  import { formatBytes, formatDateTime, formatTime, t, terr } from "../../lib/i18n/index.svelte";
  import { openMenu, type MenuItem } from "../../lib/ui/menu.svelte";
  import { tooltip } from "../../lib/ui/tooltip";
  import { toast } from "../../lib/ui/toast.svelte";
  import Avatar from "../../lib/ui/Avatar.svelte";
  import Markdown from "../../lib/ui/Markdown.svelte";
  import { isPreviewable } from "../../lib/snippet";
  import EmojiPicker from "./EmojiPicker.svelte";
  import SnippetView from "./SnippetView.svelte";

  let {
    m,
    grouped,
    canModerate,
    onReply,
    onEdit,
    onImage,
  }: {
    m: ChatMessage;
    grouped: boolean;
    canModerate: boolean;
    onReply: (m: ChatMessage) => void;
    onEdit: (m: ChatMessage) => void;
    onImage: (a: Attachment) => void;
  } = $props();

  const nodes = $derived(parse(m.content));
  const mine = $derived(m.authorId === app.me?.id);
  // The message's own copy wins for deleted accounts: the cached profile
  // may predate the deletion (e.g. missed while offline).
  const author = $derived(m.author?.deleted ? displayUser(m.author) : (app.user(m.authorId) ?? m.author));
  const mentionsMe = $derived(!!app.me && (m.mentions.includes(app.me.id) || m.mentionsEveryone) && !mine);
  let picker = $state(false);
  // Channel permissions: who may not react or write sees no such buttons.
  const rights = $derived.by(() => {
    const c = app.channels[m.channelId];
    return c ? channelRights(c) : null;
  });
  const canReact = $derived(!rights || rights.react);
  const canReply = $derived(!rights || rights.send);
  /** Text attachments opened inline. */
  let shown = $state<Record<string, boolean>>({});

  const mentionName = (id: string) => app.user(id)?.displayName ?? t("common.unknownUser");

  function react(emoji: string) {
    const r = m.reactions.find((x) => x.emoji === emoji);
    const on = !r?.me;
    if (on && !canReact) {
      toast(terr("react_denied"), "info");
      return;
    }
    // optimistic
    if (r) {
      r.count += on ? 1 : -1;
      r.me = on;
      if (r.count <= 0) m.reactions = m.reactions.filter((x) => x !== r);
    } else {
      m.reactions.push({ emoji, count: 1, me: true });
    }
    messages.react(m, emoji, on).catch((e) => toast(terr((e as { code?: string }).code), "error"));
  }

  async function del() {
    if (!window.confirm(t("chat.deleteConfirm"))) return;
    try {
      await messages.remove(m);
    } catch (e) {
      toast(terr((e as { code?: string }).code), "error");
    }
  }

  /** Text selected inside this message (right-click keeps the selection). */
  function selectedHere(e: MouseEvent): string {
    const sel = window.getSelection();
    const s = sel?.toString() ?? "";
    if (!s.trim() || !sel?.rangeCount) return "";
    const host = e.currentTarget as HTMLElement;
    return host.contains(sel.getRangeAt(0).commonAncestorContainer) ? s : "";
  }

  function copy(s: string) {
    navigator.clipboard.writeText(s).then(
      () => toast(t("toast.copied"), "success"),
      () => toast(t("toast.error"), "error"),
    );
  }

  function menu(e: MouseEvent) {
    if (m.pending) return;
    const selection = selectedHere(e);
    const items: MenuItem[] = [
      ...(selection
        ? [
            { label: t("chat.copySelection"), icon: Copy, action: () => copy(selection) },
            { separator: true as const },
          ]
        : []),
      ...(canReply ? [{ label: t("chat.reply"), icon: Reply, action: () => onReply(m) }] : []),
      ...(canReact ? [{ label: t("chat.react"), icon: SmilePlus, action: () => (picker = true) }] : []),
    ];
    if (mine) items.push({ label: t("chat.edit"), icon: Pencil, action: () => onEdit(m) });
    items.push(
      {
        label: m.pinned ? t("chat.unpin") : t("chat.pin"),
        icon: Pin,
        disabled: !canModerate,
        action: () => void messages.pin(m, !m.pinned).catch((e) => toast(terr(e.code), "error")),
      },
      { separator: true },
      {
        label: t("chat.copyText"),
        icon: Copy,
        action: () => copy(m.content),
      },
    );
    if (mine || canModerate) {
      items.push(
        { separator: true },
        { label: t("chat.delete"), icon: Trash, danger: true, action: () => void del() },
      );
    }
    openMenu(e, items);
  }

  function userMenu(e: MouseEvent) {
    openMenu(e, userMenuItems(m.authorId));
  }

  const isImage = (a: Attachment) => !!a.thumbUrl && a.mime.startsWith("image/");
</script>

<div
  class="msg"
  class:grouped
  class:mention={mentionsMe}
  class:pending={m.pending}
  role="article"
  aria-label={`${author.displayName}, ${formatTime(m.createdAt)}: ${toPlain(nodes)}`}
  oncontextmenu={menu}
  data-id={m.id}
>
  {#if m.referencedMessage}
    {@const ref = m.referencedMessage}
    <button type="button" class="ref" onclick={() => !ref.deleted && app.jumpTo(m.channelId, ref.id)}>
      <CornerUpRight size={12} strokeWidth={2} />
      <strong>{app.user(ref.authorId)?.displayName ?? t("common.unknownUser")}</strong>
      <span>{ref.deleted ? t("chat.deletedMessage") : toPlain(parse(ref.content)).slice(0, 120)}</span>
    </button>
  {/if}
  <div class="row">
    <div class="gutter">
      {#if !grouped}
        <button
          type="button"
          class="av"
          aria-label={author.displayName}
          onclick={userMenu}
          oncontextmenu={userMenu}
        >
          <Avatar
            name={author.displayName}
            id={m.authorId}
            src={author.avatarUrl && api.url(author.avatarUrl)}
            size={36}
          />
        </button>
      {:else}
        <time class="hover-time" use:tooltip={formatDateTime(m.createdAt)}>{formatTime(m.createdAt)}</time>
      {/if}
    </div>
    <div class="body">
      {#if !grouped}
        <div class="head">
          <button type="button" class="author" onclick={userMenu} oncontextmenu={userMenu}
            >{author.displayName}</button
          >
          <time use:tooltip={formatDateTime(m.createdAt)}>{formatTime(m.createdAt)}</time>
          {#if m.pinned}<Pin size={12} strokeWidth={2} />{/if}
        </div>
      {/if}
      {#if m.content}
        <div class="content selectable">
          <Markdown {nodes} mention={mentionName} />{#if m.editedAt}<span
              class="edited"
              use:tooltip={formatDateTime(m.editedAt)}
            >
              {t("chat.edited")}</span
            >{/if}
        </div>
      {/if}
      {#if m.attachments.length}
        <div class="atts">
          {#each m.attachments as a (a.id)}
            {#if isImage(a)}
              <button
                type="button"
                class="img"
                onclick={() => onImage(a)}
                aria-label={a.filename}
                style:aspect-ratio={a.width && a.height ? `${a.width} / ${a.height}` : undefined}
              >
                <img src={api.url(a.thumbUrl!)} alt={a.filename} loading="lazy" decoding="async" />
              </button>
            {:else}
              <div class="filebox">
                <div class="filerow">
                  <a class="file" href={api.url(a.url)} download={a.filename} title={a.filename}>
                    <FileIcon size={22} strokeWidth={1.5} />
                    <span class="fname">{a.filename}</span>
                    <span class="fsize">{formatBytes(a.size)}</span>
                    <Download size={16} strokeWidth={1.75} />
                  </a>
                  {#if isPreviewable(a)}
                    <button
                      type="button"
                      class="show"
                      aria-expanded={!!shown[a.id]}
                      onclick={() => (shown[a.id] = !shown[a.id])}
                      >{shown[a.id] ? t("chat.snippetHide") : t("chat.snippetShow")}</button
                    >
                  {/if}
                </div>
                {#if shown[a.id]}<SnippetView {a} />{/if}
              </div>
            {/if}
          {/each}
        </div>
      {/if}
      {#if m.reactions.length}
        <div class="reactions">
          {#each m.reactions as r (r.emoji)}
            <button type="button" class="reaction" class:me={r.me} onclick={() => react(r.emoji)}>
              <span class="emoji">{r.emoji}</span>{r.count}
            </button>
          {/each}
        </div>
      {/if}
      {#if m.failed}
        <div class="failed" role="alert">
          {terr(m.failed)}
          <button type="button" onclick={() => void messages.retry(m)}>{t("common.retry")}</button>
          <button type="button" onclick={() => messages.discard(m)}>{t("common.delete")}</button>
        </div>
      {/if}
    </div>
    {#if !m.pending}
      <div class="actions">
        {#if canReact}
          <button
            type="button"
            aria-label={t("chat.react")}
            use:tooltip={t("chat.react")}
            onclick={() => (picker = !picker)}><SmilePlus size={16} strokeWidth={1.75} /></button
          >
        {/if}
        {#if canReply}
          <button
            type="button"
            aria-label={t("chat.reply")}
            use:tooltip={t("chat.reply")}
            onclick={() => onReply(m)}><Reply size={16} strokeWidth={1.75} /></button
          >
        {/if}
        {#if mine}
          <button
            type="button"
            aria-label={t("chat.edit")}
            use:tooltip={t("chat.edit")}
            onclick={() => onEdit(m)}><Pencil size={16} strokeWidth={1.75} /></button
          >
        {/if}
      </div>
    {/if}
    {#if picker}
      <div class="picker"><EmojiPicker onpick={(e) => react(e)} onclose={() => (picker = false)} /></div>
    {/if}
  </div>
</div>

<style>
  .msg {
    position: relative;
    padding: 2px 16px 2px 0;
    margin-top: 14px;
    content-visibility: auto;
    contain-intrinsic-size: auto 48px;
  }
  .msg.grouped {
    margin-top: 0;
  }
  .msg:hover {
    background: var(--bg-hover);
  }
  /* content-visibility implies paint containment, which clips the action bar
     sticking out above the row; the hovered row is on screen anyway. */
  .msg:hover,
  .msg:focus-within {
    content-visibility: visible;
    z-index: 1;
  }
  .msg.mention {
    background: color-mix(in srgb, var(--warning) 10%, transparent);
    box-shadow: inset 2px 0 0 var(--warning);
  }
  .pending .content {
    opacity: 0.55;
  }
  .ref {
    display: flex;
    align-items: center;
    gap: 6px;
    max-width: calc(100% - 64px);
    padding: 0;
    border: none;
    background: none;
    text-align: left;
    margin-left: 64px;
    font-size: var(--fs-callout);
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
  }
  .ref span {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .row {
    display: flex;
  }
  .gutter {
    width: 64px;
    flex: none;
    display: flex;
    justify-content: center;
    padding-top: 2px;
  }
  .hover-time {
    visibility: hidden;
    font-size: 10.5px;
    color: var(--text-3);
    line-height: 22px;
  }
  .msg:hover .hover-time {
    visibility: visible;
  }
  .body {
    flex: 1;
    min-width: 0;
  }
  .head {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }
  .head :global(svg) {
    color: var(--text-3);
    align-self: center;
  }
  .author {
    padding: 0;
    border: none;
    background: none;
    color: inherit;
    font-weight: 600;
  }
  .author:hover {
    text-decoration: underline;
  }
  .av {
    padding: 0;
    border: none;
    background: none;
    border-radius: 50%;
    display: inline-flex;
  }
  .ref:hover span {
    color: var(--text-1);
  }
  .msg:global(.flash) {
    animation: flash 1.6s var(--ease-out);
  }
  @keyframes flash {
    0%,
    40% {
      background: color-mix(in srgb, var(--accent) 18%, transparent);
    }
  }
  time {
    font-size: var(--fs-caption);
    color: var(--text-3);
  }
  .content {
    font-size: var(--fs-chat);
    line-height: 1.45;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .edited {
    margin-left: 4px;
    font-size: var(--fs-caption);
    color: var(--text-3);
  }
  .atts {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 4px;
    align-items: flex-start;
  }
  .img {
    padding: 0;
    border: none;
    border-radius: var(--r-md);
    overflow: hidden;
    max-width: min(400px, 100%);
    max-height: 320px;
    background: var(--bg-active);
  }
  .img img {
    display: block;
    max-width: 100%;
    max-height: 320px;
    object-fit: contain;
  }
  .file {
    display: flex;
    align-items: center;
    gap: 10px;
    width: min(400px, 100%);
    padding: 10px 12px;
    border-radius: var(--r-md);
    border: 1px solid var(--separator);
    background: var(--bg-input);
    color: var(--text-1);
    text-decoration: none;
  }
  .filebox {
    align-self: stretch;
    min-width: 0;
  }
  .filerow {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .show {
    height: 30px;
    padding: 0 10px;
    border: 1px solid var(--separator);
    border-radius: var(--r-md);
    background: var(--bg-input);
    color: var(--text-1);
    font-size: var(--fs-callout);
    flex: none;
  }
  .show:hover {
    background: var(--bg-hover);
  }
  .file:hover {
    text-decoration: none;
    background: var(--bg-hover);
  }
  .fname {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--accent-text);
  }
  .fsize {
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .reactions {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 4px;
  }
  .reaction {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 24px;
    padding: 0 8px;
    border-radius: var(--r-full);
    border: 1px solid var(--separator);
    background: var(--bg-input);
    font-size: var(--fs-callout);
    font-variant-numeric: tabular-nums;
  }
  .reaction.me {
    border-color: color-mix(in srgb, var(--accent) 60%, transparent);
    background: color-mix(in srgb, var(--accent) 15%, transparent);
  }
  .failed {
    margin-top: 2px;
    color: var(--danger);
    font-size: var(--fs-callout);
  }
  .failed button {
    border: none;
    background: none;
    color: var(--accent-text);
    padding: 0 4px;
  }
  .actions {
    position: absolute;
    right: 16px;
    top: -14px;
    display: none;
    gap: 2px;
    padding: 2px;
    border-radius: var(--r-md);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
  }
  .msg:hover .actions,
  .msg:focus-within .actions {
    display: flex;
  }
  .actions button {
    width: 28px;
    height: 28px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-2);
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .actions button:hover {
    background: var(--bg-hover);
    color: var(--text-1);
  }
  .picker {
    position: absolute;
    right: 16px;
    top: 18px;
    z-index: 20;
  }
</style>
