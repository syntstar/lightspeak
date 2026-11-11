<script lang="ts" generics="T extends string">
  // Popup button backed by a native <select> (keyboard + screen reader for free).
  import ChevronsUpDown from "@lucide/svelte/icons/chevrons-up-down";

  let {
    value = $bindable(),
    options,
    label,
    onchange,
  }: {
    value: T;
    options: { value: T; label: string }[];
    label: string;
    onchange?: (v: T) => void;
  } = $props();
</script>

<span class="sel">
  <select aria-label={label} bind:value onchange={() => onchange?.(value)}>
    {#each options as o (o.value)}
      <option value={o.value}>{o.label}</option>
    {/each}
  </select>
  <ChevronsUpDown size={12} strokeWidth={2} />
</span>

<style>
  .sel {
    position: relative;
    display: inline-flex;
    align-items: center;
    color: var(--text-2);
  }
  select {
    appearance: none;
    height: 26px;
    padding: 0 26px 0 var(--sp-3);
    border-radius: var(--r-sm);
    border: none;
    background: var(--bg-active);
    color: var(--text-1);
    font-size: var(--fs-body);
    min-width: 140px;
  }
  select:hover {
    background: color-mix(in srgb, var(--text-1) 14%, transparent);
  }
  select:focus-visible {
    outline: 2px solid color-mix(in srgb, var(--accent) 70%, transparent);
  }
  option {
    background: var(--bg-elevated);
    color: var(--text-1);
  }
  .sel :global(svg) {
    position: absolute;
    right: 8px;
    pointer-events: none;
  }
</style>
