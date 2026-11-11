<script lang="ts">
  // Avatar with optional status dot. Falls back to initials on a colour
  // derived from the user id (stable, no network).
  import type { Status } from "../api/types";

  let {
    name,
    id = "",
    src,
    size = 32,
    status,
    speaking = false,
  }: {
    name: string;
    id?: string;
    src?: string;
    size?: number;
    status?: Status;
    speaking?: boolean;
  } = $props();

  const hues = [211, 262, 330, 28, 145, 190, 0, 45];
  const hue = $derived(
    hues[[...id].reduce((a, c) => (a * 31 + c.charCodeAt(0)) >>> 0, 7) % hues.length] ?? 211,
  );
  const initials = $derived(
    name
      .trim()
      .split(/\s+/)
      .slice(0, 2)
      .map((p) => [...p][0] ?? "")
      .join("")
      .toUpperCase() || "?",
  );
  let failed = $state(false);
  const dot = $derived(Math.max(8, Math.round(size * 0.32)));
</script>

<span class="av" class:speaking style:width="{size}px" style:height="{size}px" aria-hidden="true">
  {#if src && !failed}
    <img
      {src}
      alt=""
      width={size}
      height={size}
      loading="lazy"
      decoding="async"
      onerror={() => (failed = true)}
    />
  {:else}
    <span class="fb" style:background="hsl({hue} 55% 48%)" style:font-size="{Math.round(size * 0.4)}px"
      >{initials}</span
    >
  {/if}
  {#if status && status !== "invisible"}
    <span class="dot {status}" style:width="{dot}px" style:height="{dot}px"></span>
  {/if}
</span>

<style>
  .av {
    position: relative;
    display: inline-flex;
    flex: none;
    border-radius: 50%;
  }
  img,
  .fb {
    width: 100%;
    height: 100%;
    border-radius: 50%;
    object-fit: cover;
  }
  .fb {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: #fff;
    font-weight: 600;
    letter-spacing: 0.02em;
  }
  .speaking {
    box-shadow:
      0 0 0 2px var(--bg-content),
      0 0 0 4px var(--success);
  }
  .dot {
    position: absolute;
    right: -2px;
    bottom: -2px;
    border-radius: 50%;
    border: 2.5px solid var(--bg-sidebar-solid);
    box-sizing: content-box;
    background: var(--status-offline);
  }
  .online {
    background: var(--status-online);
  }
  .idle {
    background: var(--status-idle);
  }
  .dnd {
    background: var(--status-dnd);
  }
  .offline {
    background: var(--status-offline);
  }
</style>
