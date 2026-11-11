// Channel list order: moving channels between categories and
// categories among each other, as one PATCH /spaces/{id}/channels/order.
// Pure, so drag & drop and the context menu share it and it is testable.
import type { Channel, ID } from "./api/types";

/** One entry of the order request. parentId "0" = no category; omitted
 *  for categories (they cannot be nested). */
export interface OrderItem {
  id: ID;
  position: number;
  parentId?: ID;
}

export type Move =
  /** Channel before/after another channel (in that channel's group). */
  | { id: ID; before: ID; after?: false }
  | { id: ID; after: true; before: ID }
  /** Channel to the top of a category (null = no category). */
  | { id: ID; into: ID | null }
  /** Category before/after another category. */
  | { id: ID; category: ID; after: boolean };

const byPos = (a: Channel, b: Channel) => a.position - b.position || (a.id < b.id ? -1 : 1);

/** The new order of all given channels of one space, or null when the
 *  move is impossible or changes nothing. */
export function reorder(all: readonly Channel[], m: Move): OrderItem[] | null {
  const sorted = [...all].sort(byPos);
  const cats = sorted.filter((c) => c.kind === "category");
  const catIds = new Set(cats.map((c) => c.id));
  const groupOf = (c: Channel): ID | null => (c.parentId && catIds.has(c.parentId) ? c.parentId : null);
  const loose = sorted.filter((c) => c.kind !== "category" && groupOf(c) === null);
  const kids = new Map<ID, Channel[]>(
    cats.map((cat) => [cat.id, sorted.filter((c) => c.kind !== "category" && groupOf(c) === cat.id)]),
  );
  const list = (g: ID | null): Channel[] | undefined => (g === null ? loose : kids.get(g));
  const moving = sorted.find((c) => c.id === m.id);
  if (!moving) return null;
  // Display order before the move, to tell a no-op apart.
  const prev = [
    ...loose.map((c) => ({ id: c.id, parentId: "0" })),
    ...cats.flatMap((cat) => [
      { id: cat.id, parentId: undefined as ID | undefined },
      ...kids.get(cat.id)!.map((c) => ({ id: c.id, parentId: cat.id })),
    ]),
  ];

  if ("category" in m) {
    if (moving.kind !== "category" || m.category === m.id || !catIds.has(m.category)) return null;
    cats.splice(cats.indexOf(moving), 1);
    const at = cats.findIndex((c) => c.id === m.category) + (m.after ? 1 : 0);
    cats.splice(at, 0, moving);
  } else {
    if (moving.kind === "category") return null;
    const from = list(groupOf(moving))!;
    if ("into" in m) {
      const to = list(m.into);
      if (!to) return null;
      from.splice(from.indexOf(moving), 1);
      to.unshift(moving);
    } else {
      if (m.before === m.id) return null;
      const target = sorted.find((c) => c.id === m.before);
      if (!target || target.kind === "category") return null;
      const to = list(groupOf(target))!;
      from.splice(from.indexOf(moving), 1);
      to.splice(to.indexOf(target) + (m.after ? 1 : 0), 0, moving);
    }
  }

  const out: OrderItem[] = [];
  let pos = 0;
  for (const c of loose) out.push({ id: c.id, position: pos++, parentId: "0" });
  for (const cat of cats) {
    out.push({ id: cat.id, position: pos++ });
    for (const c of kids.get(cat.id)!) out.push({ id: c.id, position: pos++, parentId: cat.id });
  }
  const unchanged =
    out.length === prev.length &&
    out.every((o, i) => o.id === prev[i]!.id && (o.parentId ?? "0") === (prev[i]!.parentId ?? "0"));
  return unchanged ? null : out;
}
