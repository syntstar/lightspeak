// Safe Markdown subset → small AST. The renderer (Markdown.svelte) only
// creates whitelisted elements and text nodes; nothing is ever parsed as
// HTML. The parser is linear-ish: a delimiter that has no closer is
// remembered so it is never scanned for again (no quadratic blow-up), and
// nesting depth is capped.

export type MdNode =
  | { t: "text"; v: string }
  | { t: "b" | "i" | "s" | "spoiler"; c: MdNode[] }
  | { t: "code"; v: string }
  | { t: "pre"; lang: string; v: string }
  | { t: "quote"; c: MdNode[] }
  | { t: "link"; href: string; c: MdNode[] }
  | { t: "mention"; id: string }
  | { t: "everyone" }
  | { t: "br" };

const MAX_DEPTH = 6;

// Unicode bidi overrides/isolates are stripped so text cannot be visually
// reordered (e.g. disguising "exe.txt" as "txt.exe").
const BIDI = /[‪-‮⁦-⁩‎‏؜]/g;

/** Removes bidi control characters (also for code shown outside messages). */
export function stripBidi(s: string): string {
  return s.replace(BIDI, "");
}

/** Only http(s) links are allowed; returns the normalised URL or null. */
export function safeUrl(raw: string): string | null {
  try {
    const u = new URL(raw);
    if (u.protocol !== "http:" && u.protocol !== "https:") return null;
    if (u.username || u.password) return null;
    return u.href;
  } catch {
    return null;
  }
}

export function parse(src: string): MdNode[] {
  const text = src.replace(BIDI, "").replace(/\r\n?/g, "\n");
  const out: MdNode[] = [];
  const lines = text.split("\n");
  let i = 0;
  let para: string[] = [];
  const flush = () => {
    if (para.length) {
      pushLines(out, para);
      para = [];
    }
  };
  while (i < lines.length) {
    const line = lines[i]!;
    if (line.startsWith("```")) {
      const opener = /^```([\w+-]{0,20})\s*$/.exec(line);
      if (opener) {
        // Multi-line block; an unclosed fence stays plain text.
        let j = i + 1;
        while (j < lines.length && !lines[j]!.trimEnd().endsWith("```")) j++;
        if (j < lines.length) {
          flush();
          const body = lines.slice(i + 1, j);
          const rest = lines[j]!.trimEnd().slice(0, -3);
          if (rest) body.push(rest);
          out.push({ t: "pre", lang: opener[1] ?? "", v: body.join("\n") });
          i = j + 1;
          continue;
        }
      } else if (line.trimEnd().length > 6 && line.trimEnd().endsWith("```")) {
        flush();
        out.push({ t: "pre", lang: "", v: line.trimEnd().slice(3, -3) });
        i++;
        continue;
      }
    }
    if (/^>\s?/.test(line)) {
      flush();
      const q: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i]!)) q.push(lines[i++]!.replace(/^>\s?/, ""));
      const c: MdNode[] = [];
      pushLines(c, q);
      out.push({ t: "quote", c });
      continue;
    }
    para.push(line);
    i++;
  }
  flush();
  return out;
}

function pushLines(out: MdNode[], lines: string[]): void {
  lines.forEach((l, k) => {
    if (k > 0) out.push({ t: "br" });
    out.push(...inline(l, 0, new Set()));
  });
}

const DELIMS: [string, "b" | "i" | "s" | "spoiler"][] = [
  ["**", "b"],
  ["~~", "s"],
  ["||", "spoiler"],
  ["‖", "spoiler"],
  ["*", "i"],
  ["_", "i"],
];

const URL_RE = /^https?:\/\/[^\s<>"'`]+/;

function inline(s: string, depth: number, noClose: Set<string>): MdNode[] {
  const out: MdNode[] = [];
  let buf = "";
  const text = () => {
    if (buf) {
      out.push({ t: "text", v: buf });
      buf = "";
    }
  };
  let i = 0;
  while (i < s.length) {
    const ch = s[i]!;
    // escapes
    if (ch === "\\" && i + 1 < s.length && /[\\*_~`|[\]()<>@#‖]/.test(s[i + 1]!)) {
      buf += s[i + 1];
      i += 2;
      continue;
    }
    // inline code
    if (ch === "`") {
      const end = noClose.has("`") ? -1 : s.indexOf("`", i + 1);
      if (end > i + 1) {
        text();
        out.push({ t: "code", v: s.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
      if (end < 0) noClose.add("`");
    }
    // mentions
    if (ch === "<" && s[i + 1] === "@") {
      const m = /^<@(\d{1,20})>/.exec(s.slice(i, i + 24));
      if (m) {
        text();
        out.push({ t: "mention", id: m[1]! });
        i += m[0].length;
        continue;
      }
    }
    if (ch === "@" && s.startsWith("@everyone", i) && (i === 0 || /\s/.test(s[i - 1]!))) {
      text();
      out.push({ t: "everyone" });
      i += 9;
      continue;
    }
    // [label](url)
    if (ch === "[" && depth < MAX_DEPTH) {
      const close = noClose.has("]") ? -1 : s.indexOf("](", i + 1);
      if (close < 0) noClose.add("]");
      else {
        const end = s.indexOf(")", close + 2);
        const href = end > 0 ? safeUrl(s.slice(close + 2, end)) : null;
        if (href) {
          text();
          out.push({ t: "link", href, c: inline(s.slice(i + 1, close), depth + 1, new Set()) });
          i = end + 1;
          continue;
        }
      }
    }
    // bare URLs
    if (ch === "h" && (i === 0 || !/[\w/]/.test(s[i - 1]!))) {
      const m = URL_RE.exec(s.slice(i, i + 2048));
      if (m) {
        let raw = m[0];
        while (/[.,;:!?)]$/.test(raw)) raw = raw.slice(0, -1); // trailing punctuation
        const href = safeUrl(raw);
        if (href) {
          text();
          out.push({ t: "link", href, c: [{ t: "text", v: raw }] });
          i += raw.length;
          continue;
        }
      }
    }
    // emphasis
    if (depth < MAX_DEPTH) {
      let matched = false;
      for (const [d, kind] of DELIMS) {
        if (!s.startsWith(d, i) || noClose.has(d)) continue;
        // "_" only at word boundaries (snake_case stays text)
        if (d === "_" && i > 0 && /\w/.test(s[i - 1]!)) continue;
        const end = s.indexOf(d, i + d.length);
        if (end < 0) {
          noClose.add(d);
          continue;
        }
        if (end === i + d.length) continue; // empty: "****"
        if (d === "_" && end + 1 < s.length && /\w/.test(s[end + 1]!)) continue;
        text();
        out.push({ t: kind, c: inline(s.slice(i + d.length, end), depth + 1, new Set()) });
        i = end + d.length;
        matched = true;
        break;
      }
      if (matched) continue;
    }
    buf += ch;
    i++;
  }
  text();
  return out;
}

/** Plain-text preview (notifications, replies). */
export function toPlain(nodes: MdNode[], mention?: (id: string) => string): string {
  return nodes
    .map((n) => {
      switch (n.t) {
        case "text":
        case "code":
        case "pre":
          return n.v;
        case "br":
          return " ";
        case "mention":
          return mention ? `@${mention(n.id)}` : "@…";
        case "everyone":
          return "@everyone";
        case "spoiler":
          return "▒▒▒";
        default:
          return toPlain(n.c, mention);
      }
    })
    .join("");
}
