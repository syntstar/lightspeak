<script lang="ts">
  let { count }: { count: number } = $props();
</script>

{#if count > 0}
  <span class="badge">{count > 99 ? "99+" : count}</span>
{/if}

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 16px;
    height: 16px;
    padding: 0 5px;
    border-radius: var(--r-full);
    background: var(--danger);
    color: #fff;
    font-size: 10.5px;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    flex: none;
  }
</style>
