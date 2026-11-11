// In-app toasts (bottom centre). Windows toasts come in phase 3.
export interface Toast {
  id: number;
  text: string;
  kind: "info" | "success" | "error";
}

export const toasts: Toast[] = $state([]);
let next = 1;

export function toast(text: string, kind: Toast["kind"] = "info", ms = 3500): void {
  const id = next++;
  toasts.push({ id, text, kind });
  if (toasts.length > 3) toasts.shift();
  setTimeout(() => dismiss(id), ms);
}

export function dismiss(id: number): void {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}
