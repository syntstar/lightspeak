<script lang="ts">
  // Inline view of a small text attachment. Mounted only after
  // a click on "Show"; fetches the file once (size-capped) and renders it as a
  // code block. Bidi control characters are removed like in messages.
  import type { Attachment } from "../../lib/api/types";
  import { api } from "../../lib/state/app.svelte";
  import { stripBidi } from "../../lib/markdown";
  import { fileExt, MAX_PREVIEW_BYTES } from "../../lib/snippet";
  import { t } from "../../lib/i18n/index.svelte";
  import CodeBlock from "../../lib/ui/CodeBlock.svelte";

  let { a }: { a: Attachment } = $props();

  let text = $state<string | null>(null);
  let failed = $state(false);

  $effect(() => {
    const url = api.url(a.url);
    const ctl = new AbortController();
    text = null;
    failed = false;
    fetch(url, { signal: ctl.signal })
      .then(async (r) => {
        if (!r.ok) throw new Error(String(r.status));
        const buf = await r.arrayBuffer();
        if (buf.byteLength > MAX_PREVIEW_BYTES) throw new Error("too_large");
        text = stripBidi(new TextDecoder("utf-8").decode(buf)).replace(/\r\n?/g, "\n");
      })
      .catch(() => {
        if (!ctl.signal.aborted) failed = true;
      });
    return () => ctl.abort();
  });

  const lang = $derived(fileExt(a.filename));
</script>

<div class="snippet">
  {#if text !== null}
    <CodeBlock code={text} lang={lang === "txt" || lang === "log" ? "" : lang} />
  {:else if failed}
    <p class="note" role="alert">{t("chat.snippetError")}</p>
  {:else}
    <p class="note">{t("chat.snippetLoading")}</p>
  {/if}
</div>

<style>
  /* Same size as code blocks inside messages. */
  .snippet {
    font-size: var(--fs-chat);
  }
  .note {
    margin: 4px 0;
    font-size: var(--fs-callout);
    color: var(--text-2);
  }
</style>
