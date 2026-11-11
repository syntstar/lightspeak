<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import { tooltip, type Placement } from "./tooltip";

  interface Props extends HTMLButtonAttributes {
    label: string;
    active?: boolean;
    danger?: boolean;
    size?: number;
    tip?: Placement | false;
    children: Snippet;
  }
  let {
    label,
    active = false,
    danger = false,
    size = 28,
    tip = "bottom",
    children,
    ...rest
  }: Props = $props();
</script>

<button
  type="button"
  {...rest}
  class="ib"
  class:active
  class:danger
  style:width="{size}px"
  style:height="{size}px"
  aria-label={label}
  aria-pressed={active ? true : undefined}
  use:tooltip={tip ? { text: label, placement: tip } : null}
>
  {@render children()}
</button>

<style>
  .ib {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--text-2);
    border-radius: var(--r-sm);
    flex: none;
    transition:
      background-color var(--dur-fast) var(--ease-out),
      color var(--dur-fast);
  }
  .ib:hover:not(:disabled) {
    background: var(--bg-hover);
    color: var(--text-1);
  }
  .ib:active:not(:disabled) {
    background: var(--bg-active);
  }
  .active {
    color: var(--text-1);
    background: var(--bg-active);
  }
  .danger {
    color: var(--danger);
  }
  .danger:hover:not(:disabled) {
    color: var(--danger);
  }
  .ib:disabled {
    opacity: 0.4;
  }
</style>
