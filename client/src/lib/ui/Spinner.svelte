<script lang="ts">
  let { size = 16 }: { size?: number } = $props();
</script>

<span class="spinner" style:width="{size}px" style:height="{size}px" aria-hidden="true"></span>

<style>
  .spinner {
    display: inline-block;
    border-radius: 50%;
    border: 2px solid currentColor;
    border-right-color: transparent;
    opacity: 0.7;
    animation: spin 0.8s linear infinite;
    flex: none;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
