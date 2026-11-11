<script lang="ts">
  // macOS-style switch. Space/Enter toggle (native button semantics).
  let {
    checked = $bindable(false),
    label,
    disabled = false,
    onchange,
  }: { checked?: boolean; label: string; disabled?: boolean; onchange?: (v: boolean) => void } = $props();

  function flip() {
    checked = !checked;
    onchange?.(checked);
  }
</script>

<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  class="sw"
  {disabled}
  onclick={flip}
>
  <span class="knob"></span>
</button>

<style>
  .sw {
    position: relative;
    width: 32px;
    height: 19px;
    border-radius: var(--r-full);
    border: none;
    padding: 0;
    flex: none;
    background: var(--toggle-off);
    transition: background-color var(--dur-base) var(--ease-out);
  }
  .sw[aria-checked="true"] {
    background: var(--accent);
  }
  .knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 15px;
    height: 15px;
    border-radius: 50%;
    background: var(--knob);
    box-shadow:
      0 1px 2px rgba(0, 0, 0, 0.25),
      0 0 0 0.5px rgba(0, 0, 0, 0.06);
    transition: transform var(--dur-base) var(--ease-spring);
  }
  .sw[aria-checked="true"] .knob {
    transform: translateX(13px);
  }
  .sw:disabled {
    opacity: 0.5;
  }
</style>
