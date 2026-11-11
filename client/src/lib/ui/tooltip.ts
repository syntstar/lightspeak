// Tooltip action: one shared element, shown after 600 ms hover/focus.
// Usage: <button use:tooltip={"Text"}>. Pass null to disable.

let el: HTMLDivElement | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;

function ensure(): HTMLDivElement {
  if (el) return el;
  el = document.createElement("div");
  el.className = "lv-tooltip";
  el.setAttribute("role", "tooltip");
  document.body.appendChild(el);
  return el;
}

function hide(): void {
  clearTimeout(timer);
  if (el) el.dataset.show = "false";
}

function show(target: HTMLElement, text: string, placement: Placement): void {
  const t = ensure();
  t.textContent = text;
  t.dataset.show = "true";
  const r = target.getBoundingClientRect();
  const tr = t.getBoundingClientRect();
  let x: number;
  let y: number;
  if (placement === "right") {
    x = r.right + 10;
    y = r.top + r.height / 2 - tr.height / 2;
  } else if (placement === "top") {
    x = r.left + r.width / 2 - tr.width / 2;
    y = r.top - tr.height - 6;
  } else {
    x = r.left + r.width / 2 - tr.width / 2;
    y = r.bottom + 6;
  }
  x = Math.max(6, Math.min(window.innerWidth - tr.width - 6, x));
  y = Math.max(6, Math.min(window.innerHeight - tr.height - 6, y));
  t.style.transform = `translate(${Math.round(x)}px, ${Math.round(y)}px)`;
}

export type Placement = "bottom" | "top" | "right";

export function tooltip(
  node: HTMLElement,
  param: string | null | { text: string | null; placement?: Placement },
) {
  let text: string | null = null;
  let placement: Placement = "bottom";
  const set = (p: typeof param) => {
    if (p && typeof p === "object") {
      text = p.text;
      placement = p.placement ?? "bottom";
    } else {
      text = p;
    }
  };
  set(param);
  const enter = () => {
    if (!text) return;
    clearTimeout(timer);
    timer = setTimeout(() => text && show(node, text, placement), 600);
  };
  node.addEventListener("mouseenter", enter);
  node.addEventListener("mouseleave", hide);
  node.addEventListener("mousedown", hide);
  node.addEventListener("focus", enter);
  node.addEventListener("blur", hide);
  return {
    update: set,
    destroy() {
      hide();
      node.removeEventListener("mouseenter", enter);
      node.removeEventListener("mouseleave", hide);
      node.removeEventListener("mousedown", hide);
      node.removeEventListener("focus", enter);
      node.removeEventListener("blur", hide);
    },
  };
}
