<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import Spinner from "./Spinner.svelte";

  interface Props extends HTMLButtonAttributes {
    variant?: "primary" | "secondary" | "plain" | "danger";
    size?: "md" | "lg";
    busy?: boolean;
    wide?: boolean;
    children: Snippet;
  }
  let {
    variant = "secondary",
    size = "md",
    busy = false,
    wide = false,
    type = "button",
    disabled,
    children,
    ...rest
  }: Props = $props();
</script>

<button {...rest} {type} class="btn {variant} {size}" class:wide disabled={disabled || busy} aria-busy={busy}>
  {#if busy}<Spinner size={12} />{/if}
  {@render children()}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-2);
    height: 28px;
    padding: 0 var(--sp-3);
    border-radius: var(--r-sm);
    border: none;
    font-size: var(--fs-body);
    font-weight: 500;
    white-space: nowrap;
    transition:
      background-color var(--dur-fast) var(--ease-out),
      opacity var(--dur-fast);
  }
  .lg {
    height: 34px;
    padding: 0 var(--sp-4);
    border-radius: var(--r-md);
  }
  .wide {
    width: 100%;
  }
  .primary {
    background: var(--accent);
    color: var(--on-accent);
  }
  .primary:hover:not(:disabled) {
    background: color-mix(in srgb, var(--accent) 88%, black);
  }
  .secondary {
    background: var(--bg-active);
    color: var(--text-1);
  }
  .secondary:hover:not(:disabled) {
    background: color-mix(in srgb, var(--text-1) 14%, transparent);
  }
  .plain {
    background: transparent;
    color: var(--accent-text);
  }
  .plain:hover:not(:disabled) {
    background: var(--bg-hover);
  }
  .danger {
    background: var(--danger);
    color: #fff;
  }
  .danger:hover:not(:disabled) {
    background: color-mix(in srgb, var(--danger) 88%, black);
  }
  .btn:disabled {
    opacity: 0.5;
  }
  .btn:active:not(:disabled) {
    opacity: 0.85;
  }
</style>
