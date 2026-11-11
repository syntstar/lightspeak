<script lang="ts">
  // Message search (right column). Searches the current space, or everything
  // from the DM home. Filters: from:, in:, has:file (see lib/search.ts).
  import X from "@lucide/svelte/icons/x";
  import SearchIcon from "@lucide/svelte/icons/search";
  import Hash from "@lucide/svelte/icons/hash";
  import AtSign from "@lucide/svelte/icons/at-sign";
  import Paperclip from "@lucide/svelte/icons/paperclip";
  import { untrack } from "svelte";
  import CornerDownRight from "@lucide/svelte/icons/corner-down-right";
  import type { Message } from "../lib/api/types";
  import { errorCode } from "../lib/api/client";
  import { app, api, HOME } from "../lib/state/app.svelte";
  import { parse } from "../lib/markdown";
  import { parseSearch, searchParams, type SearchFilters } from "../lib/search";
  import { formatDateTime, t, terr } from "../lib/i18n/index.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";
  import Spinner from "../lib/ui/Spinner.svelte";
  import Button from "../lib/ui/Button.svelte";
  import Markdown from "../lib/ui/Markdown.svelte";

  const PAGE = 25;
  let input = $state<HTMLInputElement>();
  let results = $state<Message[] | null>(null);
  let busy = $state(false);
  let more = $state(false);
  let error = $state("");
  let filters = $state<SearchFilters | null>(null);
  let scope = $state<string | undefined>(undefined);

  $effect(() => {
    if (app.searchOpen) queueMicrotask(() => input?.focus());
  });

  // A query handed over from elsewhere (e.g. "search messages from …") runs at once.
  $effect(() => {
    if (app.searchNonce) untrack(() => void run());
  });

  function resolver() {
    const users = [...app.knownUsers(), ...(app.me ? [app.me] : [])];
    return {
      user: (n: string) => {
        const l = n.toLowerCase();
        return (
          users.find((u) => u.username === l)?.id ?? users.find((u) => u.displayName.toLowerCase() === l)?.id
        );
      },
      channel: (n: string) => {
        const l = n.toLowerCase();
        const pool =
          app.spaceId === HOME ? app.dms() : app.spaceChannels(app.spaceId).filter((c) => c.kind === "text");
        return pool.find((c) => app.channelTitle(c).toLowerCase() === l)?.id;
      },
    };
  }

  async function run(before?: string) {
    const f = parseSearch(app.searchQuery, resolver());
    scope = app.spaceId === HOME ? undefined : app.spaceId;
    const qs = searchParams(f, scope, before);
    filters = f;
    if (!qs) {
      results = null;
      return;
    }
    busy = true;
    error = "";
    try {
      const page = await api.get<Message[]>(`/search?${qs}`);
      results = before ? [...(results ?? []), ...page] : page;
      more = page.length >= PAGE;
    } catch (e) {
      error = terr(errorCode(e));
      if (!before) results = [];
    } finally {
      busy = false;
    }
  }

  function close() {
    app.searchOpen = false;
    results = null;
    filters = null;
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      void run();
    } else if (e.key === "Escape") {
      e.preventDefault();
      close();
    }
  }

  const mentionName = (id: string) => app.user(id)?.displayName ?? t("common.unknownUser");
</script>

<aside class="search" aria-label={t("search.title")}>
  <div class="head">
    <div class="field">
      <SearchIcon size={14} strokeWidth={1.75} />
      <input
        bind:this={input}
        bind:value={app.searchQuery}
        onkeydown={onKey}
        placeholder={app.spaceId === HOME ? t("search.placeholderAll") : t("search.placeholderSpace")}
        aria-label={t("search.title")}
        spellcheck="false"
      />
    </div>
    <IconButton label={t("common.close")} onclick={close}><X size={16} strokeWidth={1.75} /></IconButton>
  </div>

  <div class="body">
    {#if results === null && !busy}
      <div class="help">
        <p>{t("search.help")}</p>
        <ul>
          <li><code>{t("search.fromToken")}</code> {t("search.fromHelp")}</li>
          <li><code>{t("search.inToken")}</code> {t("search.inHelp")}</li>
          <li><code>{t("search.hasToken")}</code> {t("search.hasHelp")}</li>
        </ul>
      </div>
    {:else}
      {#if filters?.unresolved.length}
        <p class="warn">{t("search.unresolved", { tokens: filters.unresolved.join(", ") })}</p>
      {/if}
      {#if error}<p class="warn">{error}</p>{/if}
      {#if results && !busy && results.length === 0 && !error}
        <p class="none">{t("search.none")}</p>
      {/if}
      {#if results?.length}
        <p class="count">{t("search.results", { count: results.length + (more ? "+" : "") })}</p>
      {/if}
      <ul class="results">
        {#each results ?? [] as m (m.id)}
          {@const c = app.channels[m.channelId]}
          <li>
            <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
            <article
              onclick={(e) => !(e.target as HTMLElement).closest("a,button") && app.jumpTo(m.channelId, m.id)}
            >
              <span class="where">
                {#if c && (c.kind === "dm" || c.kind === "group_dm")}<AtSign
                    size={12}
                    strokeWidth={2}
                  />{:else}<Hash size={12} strokeWidth={2} />{/if}
                {c ? app.channelTitle(c) : "…"}
                {#if !scope && c?.spaceId}<span class="sp">· {app.spaces[c.spaceId]?.name}</span>{/if}
                <button type="button" class="jump" onclick={() => app.jumpTo(m.channelId, m.id)}>
                  <CornerDownRight size={12} strokeWidth={2} />{t("search.jump")}
                </button>
              </span>
              <span class="meta">
                <strong>{app.user(m.authorId)?.displayName ?? m.author?.displayName}</strong>
                <span>{formatDateTime(m.createdAt)}</span>
              </span>
              {#if m.content}
                <span class="text"><Markdown nodes={parse(m.content)} mention={mentionName} /></span>
              {/if}
              {#if m.attachments?.length}
                <span class="files"
                  ><Paperclip size={12} strokeWidth={2} />
                  {m.attachments.map((a) => a.filename).join(", ")}</span
                >
              {/if}
            </article>
          </li>
        {/each}
      </ul>
      {#if busy}
        <div class="spin"><Spinner size={16} /></div>
      {:else if more && results?.length}
        <div class="spin">
          <Button variant="plain" onclick={() => run(results?.at(-1)?.id)}>{t("search.more")}</Button>
        </div>
      {/if}
    {/if}
  </div>
</aside>

<style>
  .search {
    width: 360px;
    flex: none;
    display: flex;
    flex-direction: column;
    border-left: 1px solid var(--separator);
    background: var(--bg-content);
    min-height: 0;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 8px 8px 12px;
    border-bottom: 1px solid var(--separator);
  }
  .field {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 8px;
    border-radius: var(--r-sm);
    background: var(--bg-input);
    color: var(--text-3);
  }
  .field:focus-within {
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 35%, transparent);
  }
  .field input {
    flex: 1;
    min-width: 0;
    border: none;
    background: transparent;
    color: var(--text-1);
    outline: none;
  }
  .body {
    flex: 1;
    overflow-y: auto;
    padding: 8px 8px 16px;
  }
  .help {
    padding: 8px;
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
  .help p {
    margin: 0 0 8px;
  }
  .help ul {
    margin: 0;
    padding-left: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  code {
    font-family: var(--font-mono);
    padding: 1px 5px;
    border-radius: var(--r-xs);
    background: var(--bg-active);
    color: var(--text-1);
  }
  .warn,
  .none,
  .count {
    margin: 4px 8px 8px;
    font-size: var(--fs-callout);
    color: var(--text-2);
  }
  .warn {
    color: var(--warning);
  }
  .results {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .results article {
    cursor: default;
    display: flex;
    flex-direction: column;
    gap: 3px;
    width: 100%;
    padding: 8px 10px;
    border: 1px solid var(--separator);
    border-radius: var(--r-md);
    background: var(--bg-elevated);
    color: var(--text-1);
    text-align: left;
  }
  .results article:hover {
    border-color: var(--border-hairline);
    background: var(--bg-hover);
  }
  .where {
    display: flex;
    align-items: center;
    gap: 3px;
    font-size: var(--fs-caption);
    color: var(--text-3);
    font-weight: 600;
  }
  .sp {
    font-weight: 400;
  }
  .jump {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 1px 6px;
    border: none;
    border-radius: var(--r-xs);
    background: transparent;
    color: var(--accent-text);
    font-size: var(--fs-caption);
    font-weight: 600;
    opacity: 0;
  }
  article:hover .jump,
  .jump:focus-visible {
    opacity: 1;
  }
  .meta {
    display: flex;
    gap: 8px;
    align-items: baseline;
    font-size: var(--fs-callout);
  }
  .meta span {
    color: var(--text-3);
    font-size: var(--fs-caption);
  }
  .text {
    display: -webkit-box;
    -webkit-line-clamp: 4;
    line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
    word-break: break-word;
  }
  .files {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .spin {
    display: flex;
    justify-content: center;
    padding: 12px;
  }
</style>
