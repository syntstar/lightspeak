<script lang="ts">
  import Check from "@lucide/svelte/icons/check";
  import { closeMenu, menu, type MenuItem } from "./menu.svelte";
  import Slider from "./Slider.svelte";

  let el = $state<HTMLDivElement>();
  let pos = $state({ x: 0, y: 0 });

  type Action = Extract<MenuItem, { action: () => void }>;
  const isAction = (i: MenuItem): i is Action => "action" in i;

  $effect(() => {
    if (!menu.open || !el) return;
    // A modal <dialog> (sheets) makes everything outside it inert and sits
    // in the top layer: move the menu into the topmost one and show it as a
    // popover, so it appears above the sheet and stays clickable.
    const modal = [...document.querySelectorAll<HTMLDialogElement>("dialog[open]")].findLast((d) =>
      d.matches(":modal"),
    );
    if (modal && el.parentElement !== modal) modal.append(el);
    if (!el.matches(":popover-open")) el.showPopover();
    // Keep the menu on screen, then focus the first item.
    const r = el.getBoundingClientRect();
    pos = {
      x: Math.max(6, Math.min(menu.x, window.innerWidth - r.width - 6)),
      y: menu.y + r.height > window.innerHeight - 6 ? Math.max(6, menu.y - r.height) : menu.y,
    };
    el.querySelector<HTMLElement>("[role=menuitem]:not([disabled]), input[type=range]")?.focus();
  });

  function run(i: Action) {
    if (i.disabled) return;
    closeMenu();
    i.action();
  }

  function key(e: KeyboardEvent) {
    const items = [
      ...(el?.querySelectorAll<HTMLElement>("[role=menuitem]:not([disabled]), input[type=range]") ?? []),
    ];
    const idx = items.indexOf(document.activeElement as HTMLElement);
    const onRange = (document.activeElement as HTMLElement | null)?.tagName === "INPUT";
    if (onRange && (e.key === "ArrowLeft" || e.key === "ArrowRight" || e.key === "Home" || e.key === "End")) {
      return; // let the slider handle its own keys
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      items[(idx + 1) % items.length]?.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      items[(idx - 1 + items.length) % items.length]?.focus();
    } else if (e.key === "Home") {
      e.preventDefault();
      items[0]?.focus();
    } else if (e.key === "End") {
      e.preventDefault();
      items[items.length - 1]?.focus();
    } else if (e.key === "Escape" || e.key === "Tab") {
      e.preventDefault();
      closeMenu();
    }
  }
</script>

<svelte:window
  onblur={closeMenu}
  onresize={closeMenu}
  onmousedown={(e) => {
    if (menu.open && el && !el.contains(e.target as Node)) closeMenu();
  }}
/>

{#if menu.open}
  <div
    bind:this={el}
    class="menu"
    popover="manual"
    role="menu"
    tabindex="-1"
    style:transform="translate({pos.x}px, {pos.y}px)"
    onkeydown={key}
    oncontextmenu={(e) => e.preventDefault()}
  >
    {#each menu.items as item, i (i)}
      {#if "separator" in item}
        <div class="sep" role="separator"></div>
      {:else if "heading" in item}
        <div class="heading">{item.heading}</div>
      {:else if "slider" in item}
        <div class="slider">
          <span class="sl-lbl">{item.slider}</span>
          <Slider
            value={item.value}
            min={item.min}
            max={item.max}
            step={item.step ?? 1}
            reset={item.reset}
            label={item.slider}
            format={item.format}
            onchange={item.onchange}
          />
        </div>
      {:else if isAction(item)}
        <button
          type="button"
          role="menuitem"
          class:danger={item.danger}
          disabled={item.disabled}
          onclick={() => run(item)}
        >
          <span class="ic">
            {#if item.checked}<Check size={14} strokeWidth={2} />{:else if item.icon}<item.icon
                size={14}
                strokeWidth={1.75}
              />{/if}
          </span>
          <span class="lbl">{item.label}</span>
          {#if item.hint}<span class="hint">{item.hint}</span>{/if}
        </button>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .menu {
    position: fixed;
    inset: 0 auto auto 0;
    margin: 0;
    border: none;
    overflow: visible;
    color: inherit;
    z-index: 1000;
    min-width: 200px;
    max-width: 320px;
    padding: 5px;
    border-radius: var(--r-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    animation: pop var(--dur-base) var(--ease-spring);
    transform-origin: top left;
  }
  @keyframes pop {
    from {
      opacity: 0;
      scale: 0.96;
    }
  }
  button {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    height: 24px;
    padding: 0 8px 0 4px;
    border: none;
    border-radius: 5px;
    background: transparent;
    color: var(--text-1);
    font-size: var(--fs-body);
    text-align: left;
  }
  button:focus,
  button:hover:not(:disabled) {
    background: var(--accent);
    color: var(--on-accent);
    outline: none;
  }
  button:disabled {
    color: var(--text-3);
  }
  .danger {
    color: var(--danger);
  }
  .danger:focus,
  .danger:hover {
    background: var(--danger);
    color: #fff;
  }
  .ic {
    width: 16px;
    display: inline-flex;
    justify-content: center;
    flex: none;
  }
  .lbl {
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .hint {
    font-size: var(--fs-callout);
    opacity: 0.6;
  }
  .sep {
    height: 1px;
    margin: 4px 8px;
    background: var(--separator);
  }
  .slider {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 4px 8px 6px 26px;
  }
  .sl-lbl {
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .heading {
    padding: 4px 8px 2px 26px;
    font-size: var(--fs-caption);
    font-weight: 600;
    color: var(--text-2);
  }
</style>
