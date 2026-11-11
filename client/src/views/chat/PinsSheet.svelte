<script lang="ts">
  // Pinned messages of one channel (read-only list, newest first).
  import type { Message } from "../../lib/api/types";
  import { app, api } from "../../lib/state/app.svelte";
  import { parse } from "../../lib/markdown";
  import { formatDateTime, t, terr } from "../../lib/i18n/index.svelte";
  import Sheet from "../../lib/ui/Sheet.svelte";
  import Spinner from "../../lib/ui/Spinner.svelte";
  import Markdown from "../../lib/ui/Markdown.svelte";

  let { channelId, open = $bindable(false) }: { channelId: string; open?: boolean } = $props();

  let items = $state<Message[] | null>(null);
  let error = $state("");

  $effect(() => {
    if (!open) return;
    items = null;
    error = "";
    api
      .get<Message[]>(`/channels/${channelId}/pins`)
      .then((r) => (items = r))
      .catch((e: unknown) => (error = terr((e as { code?: string }).code)));
  });

  const mentionName = (id: string) => app.user(id)?.displayName ?? t("common.unknownUser");
</script>

<Sheet bind:open title={t("chat.pins")} width={480}>
  {#if error}
    <p class="muted">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="muted">{t("chat.noPins")}</p>
  {:else}
    <ul>
      {#each items as m (m.id)}
        <li>
          <div class="meta">
            <strong>{m.author?.displayName ?? mentionName(m.authorId)}</strong>
            <span class="muted">{formatDateTime(m.createdAt)}</span>
          </div>
          <div class="selectable"><Markdown nodes={parse(m.content)} mention={mentionName} /></div>
        </li>
      {/each}
    </ul>
  {/if}
</Sheet>

<style>
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
    max-height: 60vh;
    overflow-y: auto;
  }
  li {
    padding: 10px 12px;
    border-radius: var(--r-md);
    background: var(--bg-elevated);
  }
  .meta {
    display: flex;
    gap: 8px;
    align-items: baseline;
    margin-bottom: 4px;
  }
  .muted {
    color: var(--text-2);
  }
</style>
