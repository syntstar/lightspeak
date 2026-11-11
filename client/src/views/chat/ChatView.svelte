<script lang="ts">
  // Message list (windowed: at most WINDOW messages in the DOM, older ones
  // loaded on demand) + composer for one text channel or DM.
  import { tick } from "svelte";
  import ArrowDown from "@lucide/svelte/icons/arrow-down";
  import { toast } from "../../lib/ui/toast.svelte";
  import { can } from "../../lib/spaceAdmin.svelte";
  import { P } from "../../lib/perms";
  import type { Attachment, Channel } from "../../lib/api/types";
  import { app, api } from "../../lib/state/app.svelte";
  import { messages, type ChatMessage } from "../../lib/state/messages.svelte";
  import { idGt } from "../../lib/state/ids";
  import { formatDay, t, terr } from "../../lib/i18n/index.svelte";
  import Spinner from "../../lib/ui/Spinner.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import Composer from "./Composer.svelte";
  import MessageItem from "./MessageItem.svelte";
  import Lightbox from "./Lightbox.svelte";

  let { channel }: { channel: Channel } = $props();

  const WINDOW = 80;
  const GROUP_MS = 7 * 60 * 1000;

  let scroller = $state<HTMLDivElement>();
  let atBottom = $state(true);
  let start = $state(0); // index of the first rendered message
  let replyTo = $state<ChatMessage | null>(null);
  let editing = $state<ChatMessage | null>(null);
  let lightbox = $state<Attachment | null>(null);
  /** lastReadId when the channel was opened → "new messages" line. */
  let unreadFrom = $state<string | null>(null);

  const cache = $derived(messages.get(channel.id));
  const all = $derived(cache?.msgs ?? []);
  const shown = $derived(all.slice(start));
  const canModerate = $derived(
    channel.kind === "dm" || channel.kind === "group_dm"
      ? true
      : can(channel.spaceId ?? "", P.manageMessages),
  );

  // Channel switch: load, reset window, remember the unread position.
  $effect(() => {
    const id = channel.id;
    unreadFrom = app.readStates[id]?.lastReadId ?? null;
    replyTo = null;
    editing = null;
    atBottom = true;
    void messages.open(id).then(() => scrollToBottom());
  });

  // Keep the window at the newest WINDOW messages while following the bottom.
  $effect(() => {
    const n = all.length;
    if (atBottom) start = Math.max(0, n - WINDOW);
  });

  // Stick to the bottom when new messages arrive.
  $effect(() => {
    void all.length;
    if (atBottom) queueMicrotask(scrollToBottom);
  });

  // Read state: ack while visible at the bottom and focused.
  $effect(() => {
    void channel.lastMessageId;
    if (atBottom && app.focused && document.visibilityState === "visible") messages.ack(channel.id);
  });

  // Jump to a message (search result, reply quote): load older pages until it
  // is in the cache (bounded), render it, centre and flash it.
  let jumping = false;
  $effect(() => {
    const jt = app.jumpTarget;
    if (!jt || jt.channelId !== channel.id || !cache || cache.loading || jumping) return;
    jumping = true;
    void (async () => {
      try {
        let idx = all.findIndex((m) => m.id === jt.messageId);
        for (let i = 0; idx < 0 && i < 4 && cache.hasMore; i++) {
          if (!(await messages.loadOlder(channel.id))) break;
          idx = (messages.get(channel.id)?.msgs ?? []).findIndex((m) => m.id === jt.messageId);
        }
        if (idx < 0) {
          toast(t("search.tooOld"), "info");
          return;
        }
        atBottom = false;
        start = Math.min(start, Math.max(0, idx - 10));
        await tick();
        const el = scroller?.querySelector<HTMLElement>(`[data-id="${CSS.escape(jt.messageId)}"]`);
        el?.scrollIntoView({ block: "center" });
        el?.classList.remove("flash");
        void el?.offsetWidth;
        el?.classList.add("flash");
      } finally {
        if (app.jumpTarget === jt) app.jumpTarget = null;
        jumping = false;
      }
    })();
  });

  function scrollToBottom() {
    if (scroller) scroller.scrollTop = scroller.scrollHeight;
  }

  async function onScroll() {
    if (!scroller) return;
    const el = scroller;
    atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (el.scrollTop < 300) {
      if (start > 0) {
        start = Math.max(0, start - 40);
      } else if (cache?.hasMore && !cache.loading) {
        const before = all.length;
        await messages.loadOlder(channel.id);
        // Render the 20 newest of the freshly loaded page above the view;
        // the browser's scroll anchoring keeps the position stable.
        start = Math.max(0, all.length - before - 20);
      }
    } else if (atBottom && start < all.length - WINDOW) {
      start = Math.max(0, all.length - WINDOW);
    }
  }

  function grouped(i: number): boolean {
    const m = shown[i];
    const p = shown[i - 1];
    if (!m || !p || m.replyToId) return false;
    return (
      p.authorId === m.authorId && m.createdAt - p.createdAt < GROUP_MS && sameDay(p.createdAt, m.createdAt)
    );
  }

  function sameDay(a: number, b: number): boolean {
    return new Date(a).toDateString() === new Date(b).toDateString();
  }

  function firstUnread(i: number): boolean {
    const m = shown[i];
    const p = shown[i - 1];
    if (!m || !unreadFrom || m.authorId === app.me?.id || m.pending) return false;
    return idGt(m.id, unreadFrom) && (!p || !idGt(p.id, unreadFrom));
  }

  function editLast() {
    const last = [...all].reverse().find((m) => m.authorId === app.me?.id && !m.pending);
    if (last) editing = last;
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape" && !e.defaultPrevented && atBottom) {
      messages.ack(channel.id);
      unreadFrom = null;
    }
  }

  const typing = $derived(messages.typingUsers(channel.id).map((id) => app.user(id)?.displayName ?? "…"));
</script>

<svelte:window onkeydown={onKey} />

<div class="chat">
  <div class="scroller" bind:this={scroller} onscroll={() => void onScroll()} role="log" aria-live="polite">
    <div class="spacer"></div>
    {#if cache?.loading}
      <div class="loading"><Spinner size={16} /></div>
    {:else if cache && !cache.hasMore && start === 0}
      <div class="intro">
        <h2>{app.channelTitle(channel)}</h2>
        <p>
          {channel.kind === "dm"
            ? t("nav.dmPlaceholder", { name: app.channelTitle(channel) })
            : channel.kind === "group_dm"
              ? t("nav.groupDmPlaceholder", { name: app.channelTitle(channel) })
              : t("nav.channelPlaceholder", { name: channel.name })}
        </p>
      </div>
    {/if}
    {#if cache?.error && !all.length}
      <div class="intro err">
        <p>{terr(cache.error)}</p>
        <Button
          onclick={() => {
            messages.invalidate();
            void messages.open(channel.id);
          }}>{t("common.retry")}</Button
        >
      </div>
    {/if}
    {#each shown as m, i (m.nonce && m.pending ? m.nonce : m.id)}
      {#if i === 0 || !sameDay(shown[i - 1]!.createdAt, m.createdAt)}
        <div class="day" role="separator"><span>{formatDay(m.createdAt)}</span></div>
      {/if}
      {#if firstUnread(i)}
        <div class="unread-line" role="separator"><span>{t("chat.newMessages")}</span></div>
      {/if}
      <MessageItem
        {m}
        grouped={grouped(i) && !firstUnread(i)}
        {canModerate}
        onReply={(x) => (replyTo = x)}
        onEdit={(x) => (editing = x)}
        onImage={(a) => (lightbox = a)}
      />
    {/each}
    <div class="bottom-pad"></div>
  </div>
  {#if !atBottom}
    <button type="button" class="jump" onclick={() => ((atBottom = true), scrollToBottom())}>
      <ArrowDown size={14} strokeWidth={2} />
      {t("chat.jumpToPresent")}
    </button>
  {/if}
  <div class="typing" aria-live="polite">
    {#if typing.length}
      <span class="dots"><i></i><i></i><i></i></span>
      {typing.length === 1
        ? t("chat.typingOne", { name: typing[0]! })
        : typing.length === 2
          ? t("chat.typingTwo", { a: typing[0]!, b: typing[1]! })
          : t("chat.typingMany")}
    {/if}
  </div>
  <Composer
    {channel}
    bind:replyTo
    bind:editing
    onEditLast={editLast}
    onSent={() => ((atBottom = true), queueMicrotask(scrollToBottom))}
  />
</div>

{#if lightbox}
  <Lightbox src={api.url(lightbox.url)} name={lightbox.filename} onclose={() => (lightbox = null)} />
{/if}

<style>
  .chat {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .scroller {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    display: flex;
    flex-direction: column;
  }
  .spacer {
    flex: 1;
  }
  .bottom-pad {
    height: 8px;
    flex: none;
  }
  .loading {
    display: flex;
    justify-content: center;
    padding: 16px;
    color: var(--text-2);
  }
  .intro {
    padding: 24px 16px 8px 64px;
  }
  .intro h2 {
    margin: 0 0 4px;
    font-size: var(--fs-title1);
  }
  .intro p {
    margin: 0;
    color: var(--text-2);
  }
  .err p {
    color: var(--danger);
    margin-bottom: 8px;
  }
  .day,
  .unread-line {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 16px 16px 0 64px;
    font-size: var(--fs-caption);
    font-weight: 600;
    color: var(--text-3);
  }
  .day::after,
  .unread-line::after,
  .unread-line::before {
    content: "";
    flex: 1;
    height: 1px;
    background: var(--separator);
  }
  .unread-line {
    color: var(--danger);
  }
  .unread-line::before {
    flex: 0 0 0;
  }
  .unread-line::after {
    background: var(--danger);
  }
  .jump {
    position: absolute;
    right: 24px;
    bottom: 96px;
    display: flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 12px;
    border: none;
    border-radius: var(--r-full);
    background: var(--accent);
    color: var(--on-accent);
    box-shadow: var(--shadow-pop);
    font-size: var(--fs-callout);
    font-weight: 500;
  }
  .typing {
    height: 20px;
    padding: 0 16px;
    font-size: var(--fs-caption);
    color: var(--text-2);
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }
  .dots {
    display: inline-flex;
    gap: 2px;
  }
  .dots i {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--text-2);
    animation: blink 1.2s infinite;
  }
  .dots i:nth-child(2) {
    animation-delay: 0.2s;
  }
  .dots i:nth-child(3) {
    animation-delay: 0.4s;
  }
  @keyframes blink {
    50% {
      opacity: 0.25;
    }
  }
</style>
