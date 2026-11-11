<script lang="ts">
  // Modal sheet on a native <dialog> (focus trap, Esc, inert background).
  import type { Snippet } from "svelte";

  let {
    open = $bindable(false),
    title,
    width = 420,
    onclose,
    children,
    footer,
  }: {
    open?: boolean;
    title: string;
    width?: number;
    onclose?: () => void;
    children: Snippet;
    footer?: Snippet;
  } = $props();

  let dlg = $state<HTMLDialogElement>();

  $effect(() => {
    if (!dlg) return;
    if (open && !dlg.open) {
      dlg.showModal();
      // Chromium treats the scrolling body as focusable and may pick it as
      // the initial focus; move focus to the first real control instead.
      const a = document.activeElement;
      if (a instanceof HTMLElement && a.classList.contains("body")) {
        const first = dlg.querySelector<HTMLElement>(
          "[autofocus], input:not([disabled]), select, textarea, button:not([disabled]), [tabindex='0']",
        );
        first?.focus();
      }
    } else if (!open && dlg.open) dlg.close();
  });

  function closed() {
    if (open) {
      open = false;
      onclose?.();
    }
  }
</script>

<dialog
  bind:this={dlg}
  class="sheet"
  style:width="{width}px"
  aria-label={title}
  onclose={closed}
  onmousedown={(e) => {
    if (e.target === dlg) dlg?.close();
  }}
>
  {#if open}
    <div class="body">
      <h2>{title}</h2>
      {@render children()}
    </div>
    {#if footer}<footer>{@render footer()}</footer>{/if}
  {/if}
</dialog>

<style>
  .sheet {
    margin: min(72px, 6vh) auto auto;
    padding: 0;
    border: none;
    border-radius: var(--r-xl);
    background: var(--bg-elevated);
    color: var(--text-1);
    box-shadow: var(--shadow-sheet);
    max-width: calc(100vw - 48px);
    max-height: calc(100vh - 96px);
    overflow: hidden;
  }
  .sheet[open] {
    display: flex;
    flex-direction: column;
    animation: drop var(--dur-slow) var(--ease-spring);
  }
  .sheet::backdrop {
    background: var(--bg-overlay);
  }
  /* Body scrolls, footer buttons stay visible in small windows. */
  .body {
    min-height: 0;
    overflow-y: auto;
    padding: var(--sp-6) var(--sp-6) var(--sp-4);
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
  }
  h2 {
    margin: 0;
    font-size: var(--fs-title2);
    font-weight: 650;
  }
  footer {
    flex: none;
    display: flex;
    justify-content: flex-end;
    gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-6) var(--sp-5);
  }
  @keyframes drop {
    from {
      opacity: 0;
      transform: translateY(-12px) scale(0.98);
    }
  }
</style>
