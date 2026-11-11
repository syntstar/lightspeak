<script lang="ts">
  // Settings → Performance: live RAM/CPU of the whole app (main process plus
  // all WebView2 processes, same numbers as scripts/measure-memory.ps1), the
  // background memory saver and animations. Polls only while this pane is open.
  import { onMount } from "svelte";
  import { prefs, savePrefs } from "../../lib/prefs.svelte";
  import { formatBytes, i18n, t } from "../../lib/i18n/index.svelte";
  import { native, inTauri, type ProcStats } from "../../lib/native";
  import Button from "../../lib/ui/Button.svelte";
  import Toggle from "../../lib/ui/Toggle.svelte";

  const POLL_MS = 2000;
  const HISTORY = 30;

  let stats = $state<ProcStats | null>(null);
  let cpuHistory = $state<number[]>([]);
  let heap = $state<number | null>(null);
  let trimming = $state(false);

  async function poll() {
    try {
      const s = await native.processStats();
      stats = s;
      if (s.cpuPercent !== null) cpuHistory = [...cpuHistory, s.cpuPercent].slice(-HISTORY);
    } catch {
      stats = null;
    }
    const mem = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
    heap = mem ? mem.usedJSHeapSize : null;
  }

  onMount(() => {
    if (!inTauri) return;
    void poll();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void poll();
    }, POLL_MS);
    return () => clearInterval(id);
  });

  const pct = (v: number | null | undefined) =>
    v == null
      ? "–"
      : `${v.toLocaleString(i18n.locale, { maximumFractionDigits: 1, minimumFractionDigits: 1 })} %`;
  const avgCpu = $derived(
    cpuHistory.length ? cpuHistory.reduce((a, b) => a + b, 0) / cpuHistory.length : null,
  );
  const spark = $derived.by(() => {
    if (cpuHistory.length < 2) return "";
    const max = Math.max(5, ...cpuHistory);
    return cpuHistory.map((v, i) => `${(i / (HISTORY - 1)) * 100},${20 - (v / max) * 18}`).join(" ");
  });

  async function trim() {
    trimming = true;
    try {
      await native.memoryTrim();
      await poll();
    } finally {
      trimming = false;
    }
  }

  function setSaver(v: boolean) {
    savePrefs();
    if (inTauri) void native.setMemorySaver(v);
  }
</script>

<div class="group">
  {#if !inTauri}
    <div class="row"><span class="muted">–</span></div>
  {:else}
    <div class="row">
      <span class="two">
        {t("performance.memory")}
        <small>{t("performance.memoryHint", { count: stats?.processes ?? 0 })}</small>
      </span>
      <span class="val">{stats ? formatBytes(stats.workingSet) : "–"}</span>
    </div>
    <div class="row">
      <span class="two">
        {t("performance.private")}
        <small>{t("performance.privateHint")}</small>
      </span>
      <span class="val">{stats ? formatBytes(stats.privateBytes) : "–"}</span>
    </div>
    <div class="row">
      <span class="two">
        {t("performance.cpu")}
        <small>{t("performance.cpuHint", { avg: pct(avgCpu) })}</small>
      </span>
      <span class="cpu">
        {#if spark}
          <svg viewBox="0 0 100 20" preserveAspectRatio="none" aria-hidden="true">
            <polyline points={spark} />
          </svg>
        {/if}
        <span class="val">{pct(stats?.cpuPercent)}</span>
      </span>
    </div>
    {#if heap !== null}
      <div class="row">
        <span>{t("performance.heap")}</span>
        <span class="val">{formatBytes(heap)}</span>
      </div>
    {/if}
    <div class="row">
      <span class="two">
        {t("performance.trim")}
        <small>{t("performance.trimHint")}</small>
      </span>
      <Button busy={trimming} onclick={trim}>{t("performance.trimNow")}</Button>
    </div>
  {/if}
</div>

<div class="group">
  <div class="row">
    <span class="two">
      {t("performance.saver")}
      <small>{t("performance.saverHint")}</small>
    </span>
    <Toggle label={t("performance.saver")} bind:checked={prefs.memorySaver} onchange={setSaver} />
  </div>
  <div class="row">
    <span class="two">
      {t("settings.reduceMotion")}
      <small>{t("performance.motionHint")}</small>
    </span>
    <Toggle
      label={t("settings.reduceMotion")}
      bind:checked={prefs.reduceMotion}
      onchange={() => savePrefs()}
    />
  </div>
</div>
<p class="note">{t("performance.note")}</p>

<style>
  .val {
    font-variant-numeric: tabular-nums;
    color: var(--text-2);
    white-space: nowrap;
  }
  .cpu {
    display: inline-flex;
    align-items: center;
    gap: 10px;
  }
  svg {
    width: 100px;
    height: 20px;
  }
  polyline {
    fill: none;
    stroke: var(--accent);
    stroke-width: 1.5;
    vector-effect: non-scaling-stroke;
  }
</style>
