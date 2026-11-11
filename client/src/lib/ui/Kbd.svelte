<script lang="ts">
  let { keys }: { keys: string[] } = $props();
</script>

<span class="kbd">
  {#each keys as k, i (i)}<kbd>{k}</kbd>{/each}
</span>

<style>
  .kbd {
    display: inline-flex;
    gap: 3px;
  }
  kbd {
    font-family: var(--font-sans);
    font-size: var(--fs-caption);
    font-weight: 500;
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--r-xs);
    background: var(--bg-active);
    color: var(--text-2);
    box-shadow: inset 0 -1px 0 var(--separator);
  }
</style>
