<script lang="ts">
  // Deny / default / allow as a compact segmented control.
  // Arrow keys move the choice like any radio group.
  import X from "@lucide/svelte/icons/x";
  import Slash from "@lucide/svelte/icons/slash";
  import Check from "@lucide/svelte/icons/check";
  import type { TriState } from "../channelRules";
  import { t } from "../i18n/index.svelte";

  let {
    value,
    label,
    disabled = false,
    onchange,
  }: {
    value: TriState;
    label: string;
    disabled?: boolean;
    onchange: (v: TriState) => void;
  } = $props();

  const order: TriState[] = ["deny", "inherit", "allow"];
  const names = $derived({
    deny: t("chperm.deny"),
    inherit: t("chperm.inherit"),
    allow: t("chperm.allow"),
  });

  function key(e: KeyboardEvent) {
    if (disabled) return;
    const i = order.indexOf(value);
    let n: number;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") n = Math.min(i + 1, 2);
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") n = Math.max(i - 1, 0);
    else return;
    e.preventDefault();
    if (n === i) return;
    onchange(order[n]!);
    ((e.currentTarget as HTMLElement).children[n] as HTMLElement | undefined)?.focus();
  }
</script>

<div class="tri" role="radiogroup" aria-label={label} tabindex="-1" onkeydown={key}>
  {#each order as o (o)}
    <button
      type="button"
      role="radio"
      class={o}
      aria-checked={o === value}
      aria-label={names[o]}
      title={names[o]}
      tabindex={o === value ? 0 : -1}
      {disabled}
      onclick={() => o !== value && onchange(o)}
    >
      {#if o === "deny"}<X size={14} strokeWidth={2.25} />{:else if o === "inherit"}<Slash
          size={12}
          strokeWidth={2.25}
        />{:else}<Check size={14} strokeWidth={2.25} />{/if}
    </button>
  {/each}
</div>

<style>
  .tri {
    display: inline-flex;
    flex: none;
    padding: 2px;
    gap: 2px;
    border-radius: var(--r-md);
    background: var(--bg-active);
  }
  button {
    display: grid;
    place-items: center;
    width: 30px;
    height: 24px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-3);
    transition:
      background-color var(--dur-fast) var(--ease-out),
      color var(--dur-fast);
  }
  button:not(:disabled):hover {
    color: var(--text-1);
  }
  button:disabled {
    opacity: 0.5;
  }
  button[aria-checked="true"] {
    box-shadow:
      0 1px 2px rgba(0, 0, 0, 0.12),
      0 0 0 0.5px var(--border-hairline);
  }
  .inherit[aria-checked="true"] {
    background: var(--bg-elevated);
    color: var(--text-1);
  }
  .deny[aria-checked="true"] {
    background: var(--danger);
    color: #fff;
  }
  .allow[aria-checked="true"] {
    background: var(--success);
    color: #fff;
  }
  button:disabled[aria-checked="true"] {
    opacity: 0.6;
  }
</style>
