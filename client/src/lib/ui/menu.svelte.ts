// Context menu system: openMenu(event, items) from any oncontextmenu /
// button; ContextMenuHost renders a single macOS-style menu.
import type { Component } from "svelte";

export type MenuItem =
  | {
      label: string;
      action: () => void;
      icon?: Component<{ size?: number; strokeWidth?: number }>;
      danger?: boolean;
      disabled?: boolean;
      checked?: boolean;
      hint?: string;
    }
  | { separator: true }
  | { heading: string }
  | {
      /** Inline range control (e.g. per-user volume); the menu stays open. */
      slider: string;
      value: number;
      min: number;
      max: number;
      step?: number;
      reset?: number;
      format?: (v: number) => string;
      onchange: (v: number) => void;
    };

interface MenuState {
  open: boolean;
  x: number;
  y: number;
  items: MenuItem[];
  returnFocus: HTMLElement | null;
}

export const menu: MenuState = $state({ open: false, x: 0, y: 0, items: [], returnFocus: null });

export function openMenu(e: MouseEvent | KeyboardEvent, items: MenuItem[]): void {
  e.preventDefault();
  e.stopPropagation();
  let x: number;
  let y: number;
  if (e instanceof MouseEvent && e.type !== "click") {
    x = e.clientX;
    y = e.clientY;
  } else {
    // Keyboard / button click: anchor below the element.
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    x = r.left;
    y = r.bottom + 4;
  }
  menu.returnFocus = document.activeElement as HTMLElement | null;
  menu.items = items;
  menu.x = x;
  menu.y = y;
  menu.open = true;
}

/** Opens a menu below an element (for items loaded asynchronously). */
export function openMenuAt(el: HTMLElement, items: MenuItem[]): void {
  const r = el.getBoundingClientRect();
  menu.returnFocus = el;
  menu.items = items;
  menu.x = r.left;
  menu.y = r.bottom + 4; // the host flips it up when it would overflow
  menu.open = true;
}

export function closeMenu(): void {
  if (!menu.open) return;
  menu.open = false;
  menu.returnFocus?.focus?.();
  menu.returnFocus = null;
}
