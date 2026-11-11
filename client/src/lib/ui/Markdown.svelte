<script lang="ts">
  // Renders the safe Markdown AST. Only text nodes and a fixed element
  // whitelist; links open in the default browser (never in the webview).
  import type { MdNode } from "../markdown";
  import { openLink } from "../links";
  import Self from "./Markdown.svelte";
  import CodeBlock from "./CodeBlock.svelte";

  let { nodes, mention }: { nodes: MdNode[]; mention: (id: string) => string } = $props();
</script>

{#each nodes as n, i (i)}
  {#if n.t === "text"}{n.v}{:else if n.t === "br"}<br />{:else if n.t === "b"}<strong
      ><Self nodes={n.c} {mention} /></strong
    >{:else if n.t === "i"}<em><Self nodes={n.c} {mention} /></em>{:else if n.t === "s"}<s
      ><Self nodes={n.c} {mention} /></s
    >{:else if n.t === "spoiler"}<button
      type="button"
      class="spoiler"
      onclick={(e) => (e.currentTarget as HTMLElement).classList.add("shown")}
      ><Self nodes={n.c} {mention} /></button
    >{:else if n.t === "code"}<code>{n.v}</code>{:else if n.t === "pre"}<CodeBlock
      code={n.v}
      lang={n.lang}
    />{:else if n.t === "quote"}<blockquote>
      <Self nodes={n.c} {mention} />
    </blockquote>{:else if n.t === "link"}<a
      href={n.href}
      rel="noreferrer noopener"
      title={n.href}
      onclick={(e) => {
        e.preventDefault();
        openLink(n.href);
      }}><Self nodes={n.c} {mention} /></a
    >{:else if n.t === "mention"}<span class="mention">@{mention(n.id)}</span
    >{:else if n.t === "everyone"}<span class="mention">@everyone</span>{/if}
{/each}

<style>
  code {
    font-family: var(--font-mono);
    font-size: 0.88em;
    padding: 1px 4px;
    border-radius: var(--r-xs);
    background: var(--bg-active);
  }
  blockquote {
    margin: 2px 0;
    padding: 0 0 0 10px;
    border-left: 3px solid var(--separator);
    color: var(--text-2);
  }
  .mention {
    padding: 0 3px;
    border-radius: var(--r-xs);
    background: color-mix(in srgb, var(--accent) 20%, transparent);
    color: var(--accent-text);
    font-weight: 500;
  }
  .spoiler {
    border: none;
    padding: 0 2px;
    border-radius: var(--r-xs);
    background: var(--text-2);
    color: transparent;
    font: inherit;
  }
  .spoiler :global(*) {
    visibility: hidden;
  }
  :global(.spoiler.shown) {
    background: var(--bg-active);
    color: inherit;
  }
  :global(.spoiler.shown *) {
    visibility: visible;
  }
</style>
