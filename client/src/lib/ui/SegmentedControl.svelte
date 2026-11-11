<script lang="ts" generics="T extends string">
  // Radio group styled as a segmented control; arrow keys move the choice.
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

  function pick(v: T) {
    if (v === value) return;
    value = v;
    onchange?.(v);
  }

  function key(e: KeyboardEvent) {
    const i = options.findIndex((o) => o.value === value);
    let n: number;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") n = (i + 1) % options.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") n = (i - 1 + options.length) % options.length;
    else return;
    e.preventDefault();
    const o = options[n];
    if (o) {
      pick(o.value);
      ((e.currentTarget as HTMLElement).children[n] as HTMLElement | undefined)?.focus();
    }
  }
</script>

<div class="seg" role="radiogroup" aria-label={label} tabindex="-1" onkeydown={key}>
  {#each options as o (o.value)}
    <button
      type="button"
      role="radio"
      aria-checked={o.value === value}
      tabindex={o.value === value ? 0 : -1}
      onclick={() => pick(o.value)}>{o.label}</button
    >
  {/each}
</div>

<style>
  .seg {
    display: inline-flex;
    padding: 2px;
    gap: 2px;
    border-radius: var(--r-md);
    background: var(--bg-active);
  }
  button {
    border: none;
    background: transparent;
    height: 24px;
    padding: 0 var(--sp-3);
    border-radius: var(--r-sm);
    font-size: var(--fs-callout);
    font-weight: 500;
    color: var(--text-2);
    transition:
      background-color var(--dur-fast) var(--ease-out),
      color var(--dur-fast);
  }
  button:hover {
    color: var(--text-1);
  }
  button[aria-checked="true"] {
    background: var(--bg-elevated);
    color: var(--text-1);
    box-shadow:
      0 1px 2px rgba(0, 0, 0, 0.12),
      0 0 0 0.5px var(--border-hairline);
  }
</style>
