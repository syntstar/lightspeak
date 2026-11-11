// The built-in WebView2/browser context menu ("Back", "Print", …)
// never appears in the app – except where it is useful: text fields, editable
// content, selected text (copy/paste) and links in the browser client.

interface Closest {
  closest(selector: string): unknown;
}

const EDITABLE = "input, textarea, [contenteditable=''], [contenteditable='true']";

/** Whether the native context menu should open for this target. */
export function allowNativeMenu(target: Closest | null, selection: string, web: boolean): boolean {
  if (selection.trim()) return true;
  if (!target || typeof target.closest !== "function") return false;
  if (target.closest(EDITABLE)) return true;
  return web && !!target.closest("a[href]");
}

export function suppressNativeMenu(web: boolean): void {
  window.addEventListener("contextmenu", (e) => {
    // Our own menus call preventDefault() + stopPropagation() before this.
    const target = e.target instanceof Element ? e.target : null;
    if (!allowNativeMenu(target, window.getSelection()?.toString() ?? "", web)) e.preventDefault();
  });
}
