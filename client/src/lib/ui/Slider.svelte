<script lang="ts">
  // Range slider with value read-out; double-click resets to `reset`.
  let {
    value = $bindable(),
    min = 0,
    max = 100,
    step = 1,
    reset,
    label,
    format = (v: number) => String(v),
    onchange,
  }: {
    value: number;
    min?: number;
    max?: number;
    step?: number;
    reset?: number;
    label: string;
    format?: (v: number) => string;
    onchange?: (v: number) => void;
  } = $props();

  const pct = $derived(((value - min) / (max - min)) * 100);
</script>

<span class="sl">
  <input
    type="range"
    aria-label={label}
    {min}
    {max}
    {step}
    bind:value
    style:--pct="{pct}%"
    oninput={() => onchange?.(value)}
    ondblclick={() => {
      if (reset !== undefined) {
        value = reset;
        onchange?.(value);
      }
    }}
  />
  <output>{format(value)}</output>
</span>

<style>
  .sl {
    display: inline-flex;
    align-items: center;
    gap: var(--sp-3);
  }
  input {
    appearance: none;
    width: 160px;
    height: 4px;
    border-radius: var(--r-full);
    background: linear-gradient(to right, var(--accent) var(--pct), var(--bg-active) var(--pct));
  }
  input::-webkit-slider-thumb {
    appearance: none;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--knob);
    box-shadow:
      0 1px 3px rgba(0, 0, 0, 0.3),
      0 0 0 0.5px rgba(0, 0, 0, 0.1);
  }
  output {
    min-width: 40px;
    text-align: right;
    font-variant-numeric: tabular-nums;
    color: var(--text-2);
    font-size: var(--fs-callout);
  }
</style>
