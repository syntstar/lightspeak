<script lang="ts">
  import type { HTMLInputAttributes } from "svelte/elements";

  interface Props extends HTMLInputAttributes {
    value?: string;
    label?: string;
    hint?: string;
    error?: string | null;
    el?: HTMLInputElement;
  }
  let { value = $bindable(""), label, hint, error = null, el = $bindable(), id, ...rest }: Props = $props();
  const uid = $props.id();
  const fid = $derived(id ?? `tf-${uid}`);
</script>

<label class="tf" for={fid}>
  {#if label}<span class="lbl">{label}</span>{/if}
  <input
    {...rest}
    id={fid}
    bind:this={el}
    bind:value
    aria-invalid={error ? true : undefined}
    aria-describedby={hint || error ? `${fid}-d` : undefined}
  />
  {#if error}
    <span class="msg err" id="{fid}-d" role="alert">{error}</span>
  {:else if hint}
    <span class="msg" id="{fid}-d">{hint}</span>
  {/if}
</label>

<style>
  .tf {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .lbl {
    font-size: var(--fs-callout);
    font-weight: 600;
    color: var(--text-2);
  }
  input {
    height: 32px;
    padding: 0 var(--sp-3);
    border-radius: var(--r-md);
    border: 1px solid var(--border-hairline);
    background: var(--bg-input);
    color: var(--text-1);
    font-size: var(--fs-body);
    transition:
      box-shadow var(--dur-fast) var(--ease-out),
      border-color var(--dur-fast);
  }
  input::placeholder {
    color: var(--text-3);
  }
  input:focus {
    border-color: color-mix(in srgb, var(--accent) 70%, transparent);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 35%, transparent);
  }
  input[aria-invalid="true"] {
    border-color: var(--danger);
  }
  .msg {
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .err {
    color: var(--danger);
  }
</style>
