<script lang="ts">
  // Ctrl+K: fuzzy jump to channels, DMs and spaces.
  import Hash from "@lucide/svelte/icons/hash";
  import Volume2 from "@lucide/svelte/icons/volume-2";
  import AtSign from "@lucide/svelte/icons/at-sign";
  import LayoutGrid from "@lucide/svelte/icons/layout-grid";
  import { app, HOME } from "../lib/state/app.svelte";
  import { fuzzyScore } from "../lib/state/ids";
  import { t } from "../lib/i18n/index.svelte";

  interface Entry {
    id: string;
    kind: "channel" | "voice" | "dm" | "space";
    label: string;
    sub: string;
    unread: boolean;
  }

  let q = $state("");
  let sel = $state(0);
  let dlg = $state<HTMLDialogElement>();
  let input = $state<HTMLInputElement>();

  const entries = $derived.by((): Entry[] => {
    const out: Entry[] = [];
    for (const c of Object.values(app.channels)) {
      if (c.kind === "category") continue;
      const dm = c.kind === "dm" || c.kind === "group_dm";
      out.push({
        id: c.id,
        kind: dm ? "dm" : c.kind === "voice" ? "voice" : "channel",
        label: app.channelTitle(c),
        sub: dm ? t("nav.dms") : (app.spaces[c.spaceId ?? ""]?.name ?? ""),
        unread: app.isUnread(c),
      });
    }
    for (const id of app.spaceOrder) {
      const s = app.spaces[id];
      if (s) out.push({ id, kind: "space", label: s.name, sub: "", unread: app.spaceUnread(id).unread });
    }
    return out;
  });

  const results = $derived.by(() => {
    if (!q.trim()) {
      // Unread first, then everything else.
      return [...entries].sort((a, b) => Number(b.unread) - Number(a.unread)).slice(0, 30);
    }
    const query = q.trim().replace(/^[#@]/, "");
    return entries
      .map((e) => ({ e, s: fuzzyScore(query, e.label) }))
      .filter((x) => x.s >= 0)
      .sort((a, b) => b.s - a.s + (Number(b.e.unread) - Number(a.e.unread)) * 0.5)
      .slice(0, 30)
      .map((x) => x.e);
  });

  $effect(() => {
    if (!dlg) return;
    if (app.quickSwitcher && !dlg.open) {
      q = "";
      sel = 0;
      dlg.showModal();
      input?.focus();
    } else if (!app.quickSwitcher && dlg.open) {
      dlg.close();
    }
  });

  $effect(() => {
    void q;
    sel = 0;
  });

  function go(e: Entry | undefined) {
    if (!e) return;
    app.quickSwitcher = false;
    if (e.kind === "space") app.selectSpace(e.id);
    else if (e.kind !== "voice") app.selectChannel(e.id);
    else {
      const sid = app.channels[e.id]?.spaceId;
      if (sid) app.selectSpace(sid);
    }
  }

  function key(e: KeyboardEvent) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      sel = Math.min(results.length - 1, sel + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      sel = Math.max(0, sel - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(results[sel]);
    }
  }

  $effect(() => {
    document.getElementById(`qs-${sel}`)?.scrollIntoView({ block: "nearest" });
  });
</script>

<dialog
  bind:this={dlg}
  class="qs"
  aria-label={t("nav.quickSwitch")}
  onclose={() => (app.quickSwitcher = false)}
  onmousedown={(e) => {
    if (e.target === dlg) app.quickSwitcher = false;
  }}
>
  {#if app.quickSwitcher}
    <input
      bind:this={input}
      bind:value={q}
      placeholder={t("qs.placeholder")}
      onkeydown={key}
      spellcheck="false"
      role="combobox"
      aria-expanded="true"
      aria-controls="qs-list"
      aria-activedescendant={results.length ? `qs-${sel}` : undefined}
    />
    <div class="results" id="qs-list" role="listbox">
      {#each results as r, i (r.kind + r.id)}
        <div
          id="qs-{i}"
          role="option"
          tabindex="-1"
          aria-selected={i === sel}
          class="r"
          class:sel={i === sel}
          class:unread={r.unread}
          onmousemove={() => (sel = i)}
          onclick={() => go(r)}
          onkeydown={() => {}}
        >
          {#if r.kind === "channel"}<Hash size={16} strokeWidth={1.75} />{:else if r.kind === "voice"}<Volume2
              size={16}
              strokeWidth={1.75}
            />{:else if r.kind === "dm"}<AtSign size={16} strokeWidth={1.75} />{:else}<LayoutGrid
              size={16}
              strokeWidth={1.75}
            />{/if}
          <span class="lbl">{r.label}</span>
          {#if r.sub && r.id !== HOME}<span class="sub">{r.sub}</span>{/if}
        </div>
      {:else}
        <p class="empty">{t("qs.empty")}</p>
      {/each}
    </div>
    <footer>{t("qs.hint")}</footer>
  {/if}
</dialog>

<style>
  .qs {
    width: 560px;
    max-width: calc(100vw - 48px);
    margin: 96px auto auto;
    padding: 0;
    border: none;
    border-radius: var(--r-xl);
    background: var(--bg-elevated);
    color: var(--text-1);
    box-shadow: var(--shadow-sheet);
    overflow: hidden;
  }
  .qs[open] {
    animation: pop var(--dur-base) var(--ease-spring);
  }
  .qs::backdrop {
    background: var(--bg-overlay);
  }
  @keyframes pop {
    from {
      opacity: 0;
      transform: scale(0.97);
    }
  }
  input {
    width: 100%;
    height: 52px;
    padding: 0 20px;
    border: none;
    border-bottom: 1px solid var(--separator);
    background: transparent;
    font-size: var(--fs-title3);
  }
  input::placeholder {
    color: var(--text-3);
  }
  .results {
    max-height: 360px;
    overflow-y: auto;
    padding: 6px;
  }
  .r {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 34px;
    padding: 0 12px;
    border-radius: var(--r-md);
    color: var(--text-2);
  }
  .r.unread {
    color: var(--text-1);
    font-weight: 600;
  }
  .r.sel {
    background: var(--accent);
    color: var(--on-accent);
  }
  .lbl {
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub {
    font-size: var(--fs-callout);
    opacity: 0.7;
    font-weight: 400;
  }
  .empty {
    margin: 16px;
    color: var(--text-3);
  }
  footer {
    padding: 8px 16px;
    border-top: 1px solid var(--separator);
    font-size: var(--fs-caption);
    color: var(--text-3);
  }
</style>
