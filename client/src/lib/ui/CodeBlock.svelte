<script lang="ts">
  // Code block: lazy syntax highlighting, copy button and
  // long blocks folded to COLLAPSED_LINES with "show more". The <code>
  // element is filled by the action only (text first, highlighted spans once
  // the highlighter chunk is loaded) – Svelte never renders into it.
  import Copy from "@lucide/svelte/icons/copy";
  import { t, tp } from "../i18n/index.svelte";
  import { toast } from "./toast.svelte";
  import { tooltip } from "./tooltip";

  const COLLAPSED_LINES = 15;

  let { code, lang = "" }: { code: string; lang?: string } = $props();

  const lines = $derived(code.split("\n").length);
  const foldable = $derived(lines > COLLAPSED_LINES + 3);
  let expanded = $state(false);
  const label = $derived(lang.trim().toLowerCase().slice(0, 20));

  function fill(node: HTMLElement, p: { code: string; lang: string }) {
    let seq = 0;
    function run(q: { code: string; lang: string }) {
      const mine = ++seq;
      node.textContent = q.code;
      if (!q.lang.trim()) return;
      import("../highlight")
        .then((m) => {
          if (mine !== seq) return;
          const nodes = m.highlight(q.code, q.lang);
          if (nodes) node.replaceChildren(nodes);
        })
        .catch(() => {}); // stays plain text
    }
    run(p);
    return {
      update: run,
      destroy() {
        seq++;
      },
    };
  }

  function copy() {
    void navigator.clipboard.writeText(code).then(
      () => toast(t("toast.copied"), "success"),
      () => toast(t("toast.error"), "error"),
    );
  }
</script>

<div class="cb" class:folded={foldable && !expanded}>
  <pre class="selectable"><code class="hl" use:fill={{ code, lang }}></code></pre>
  <div class="tools">
    {#if label}<span class="lang">{label}</span>{/if}
    <button type="button" class="copy" aria-label={t("code.copy")} use:tooltip={t("code.copy")} onclick={copy}
      ><Copy size={14} strokeWidth={1.75} /></button
    >
  </div>
  {#if foldable}
    <button type="button" class="more" aria-expanded={expanded} onclick={() => (expanded = !expanded)}>
      {expanded ? t("code.showLess") : tp("code.showMore", lines - COLLAPSED_LINES)}
    </button>
  {/if}
</div>

<style>
  .cb {
    position: relative;
    margin: 4px 0;
    max-width: 100%;
    border-radius: var(--r-md);
    background: var(--bg-input);
    border: 1px solid var(--separator);
    white-space: normal;
  }
  pre {
    margin: 0;
    padding: 8px 10px;
    overflow-x: auto;
    white-space: pre;
    font-family: var(--font-mono);
    font-size: 0.88em;
    line-height: 1.45;
    /* Show code as typed: no "=>" → "⇒" ligatures. */
    font-variant-ligatures: none;
  }
  .folded pre {
    max-height: calc(15lh + 8px);
    overflow-y: hidden;
    mask-image: linear-gradient(to bottom, #000 calc(100% - 3lh), transparent);
  }
  code {
    font-family: inherit;
  }
  .tools {
    position: absolute;
    top: 4px;
    right: 4px;
    display: flex;
    align-items: center;
    gap: 4px;
    opacity: 0;
    transition: opacity var(--dur-fast);
  }
  .cb:hover .tools,
  .cb:focus-within .tools {
    opacity: 1;
  }
  .lang {
    font-size: var(--fs-caption);
    color: var(--text-3);
    padding: 0 4px;
  }
  .copy {
    width: 26px;
    height: 26px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    border-radius: var(--r-sm);
    background: var(--bg-elevated);
    color: var(--text-2);
    box-shadow: 0 0 0 0.5px var(--border-hairline);
  }
  .copy:hover {
    color: var(--text-1);
  }
  .more {
    display: block;
    width: 100%;
    padding: 4px 10px 6px;
    border: none;
    border-top: 1px solid var(--separator);
    background: transparent;
    color: var(--accent-text);
    font-size: var(--fs-callout);
    text-align: left;
  }
  .more:hover {
    background: var(--bg-hover);
  }

  /* Syntax colours (Xcode-like), see tokens.css for --syn-*. */
  .hl :global(.hljs-keyword),
  .hl :global(.hljs-selector-tag),
  .hl :global(.hljs-literal),
  .hl :global(.hljs-doctag),
  .hl :global(.hljs-meta .hljs-keyword) {
    color: var(--syn-keyword);
  }
  .hl :global(.hljs-string),
  .hl :global(.hljs-regexp),
  .hl :global(.hljs-template-tag),
  .hl :global(.hljs-char) {
    color: var(--syn-string);
  }
  .hl :global(.hljs-number),
  .hl :global(.hljs-symbol),
  .hl :global(.hljs-bullet) {
    color: var(--syn-number);
  }
  .hl :global(.hljs-comment),
  .hl :global(.hljs-quote) {
    color: var(--syn-comment);
    font-style: italic;
  }
  .hl :global(.hljs-title),
  .hl :global(.hljs-section),
  .hl :global(.hljs-selector-id),
  .hl :global(.hljs-selector-class) {
    color: var(--syn-title);
  }
  .hl :global(.hljs-type),
  .hl :global(.hljs-built_in),
  .hl :global(.hljs-class .hljs-title),
  .hl :global(.hljs-title.class_) {
    color: var(--syn-type);
  }
  .hl :global(.hljs-attr),
  .hl :global(.hljs-attribute),
  .hl :global(.hljs-variable),
  .hl :global(.hljs-template-variable),
  .hl :global(.hljs-property),
  .hl :global(.hljs-params) {
    color: var(--syn-attr);
  }
  .hl :global(.hljs-meta),
  .hl :global(.hljs-name),
  .hl :global(.hljs-tag) {
    color: var(--syn-meta);
  }
  .hl :global(.hljs-addition) {
    color: var(--syn-add);
    background: color-mix(in srgb, var(--syn-add) 14%, transparent);
  }
  .hl :global(.hljs-deletion) {
    color: var(--syn-del);
    background: color-mix(in srgb, var(--syn-del) 14%, transparent);
  }
  .hl :global(.hljs-emphasis) {
    font-style: italic;
  }
  .hl :global(.hljs-strong) {
    font-weight: 650;
  }
</style>
