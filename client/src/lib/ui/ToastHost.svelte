<script lang="ts">
  import CircleCheck from "@lucide/svelte/icons/circle-check";
  import CircleAlert from "@lucide/svelte/icons/circle-alert";
  import { dismiss, toasts } from "./toast.svelte";
</script>

<div class="host" role="status" aria-live="polite">
  {#each toasts as t (t.id)}
    <button type="button" class="toast {t.kind}" onclick={() => dismiss(t.id)}>
      {#if t.kind === "success"}<CircleCheck size={16} strokeWidth={1.75} />{/if}
      {#if t.kind === "error"}<CircleAlert size={16} strokeWidth={1.75} />{/if}
      <span>{t.text}</span>
    </button>
  {/each}
</div>

<style>
  .host {
    position: fixed;
    left: 50%;
    bottom: 24px;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    z-index: 1100;
    pointer-events: none;
  }
  .toast {
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: 8px;
    max-width: 480px;
    padding: 8px 14px;
    border: none;
    border-radius: var(--r-lg);
    background: var(--bg-elevated);
    color: var(--text-1);
    box-shadow: var(--shadow-pop);
    font-size: var(--fs-body);
    text-align: left;
    animation: up var(--dur-base) var(--ease-spring);
  }
  .success :global(svg) {
    color: var(--success-text);
  }
  .error :global(svg) {
    color: var(--danger);
  }
  @keyframes up {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
  }
</style>
