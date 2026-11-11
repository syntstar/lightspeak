// Snowflake IDs arrive as decimal strings; compare without precision loss.
export function idCmp(a: string, b: string): number {
  if (a.length !== b.length) return a.length - b.length;
  return a < b ? -1 : a > b ? 1 : 0;
}

export function idGt(a: string | undefined, b: string | undefined): boolean {
  if (!a) return false;
  if (!b) return true;
  return idCmp(a, b) > 0;
}

/** Simple fuzzy score: all query chars in order; earlier/contiguous is better.
 *  Returns -1 when the query does not match. */
export function fuzzyScore(query: string, text: string): number {
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  if (!q) return 0;
  const direct = t.indexOf(q);
  if (direct >= 0) return 1000 - direct * 2 - (t.length - q.length) * 0.1;
  let score = 0;
  let ti = 0;
  let last = -2;
  for (const ch of q) {
    const idx = t.indexOf(ch, ti);
    if (idx < 0) return -1;
    score += idx === last + 1 ? 8 : 1;
    if (idx === 0 || /[\s\-_.#]/.test(t[idx - 1] ?? "")) score += 5;
    last = idx;
    ti = idx + 1;
  }
  return score - t.length * 0.1;
}
