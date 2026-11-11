// Search query parsing: free text plus the filters `from:<user>`,
// `in:<channel>` and `has:file` (German aliases `von:`, `in:`, `hat:datei`).
// Names are resolved by the caller; unknown names stay in the text so the
// user sees no silent filter.

export interface SearchFilters {
  text: string;
  authorId?: string;
  channelId?: string;
  hasFile?: boolean;
  /** Filter tokens that could not be resolved (shown as a hint). */
  unresolved: string[];
}

export interface SearchResolver {
  user(name: string): string | undefined;
  channel(name: string): string | undefined;
}

const TOKEN = /(?:^|\s)(from|von|in|has|hat):(?:"([^"]*)"|(\S+))/gi;

export function parseSearch(input: string, r: SearchResolver): SearchFilters {
  const out: SearchFilters = { text: "", unresolved: [] };
  const rest = input.replace(
    TOKEN,
    (whole, key: string, quoted: string | undefined, bare: string | undefined) => {
      const k = key.toLowerCase();
      const v = (quoted ?? bare ?? "").trim();
      if (!v) return whole;
      if (k === "from" || k === "von") {
        const id = r.user(v.replace(/^@/, ""));
        if (id) out.authorId = id;
        else out.unresolved.push(whole.trim());
      } else if (k === "in") {
        const id = r.channel(v.replace(/^#/, ""));
        if (id) out.channelId = id;
        else out.unresolved.push(whole.trim());
      } else {
        const f = v.toLowerCase();
        if (f === "file" || f === "datei" || f === "attachment" || f === "anhang") out.hasFile = true;
        else out.unresolved.push(whole.trim());
      }
      return " ";
    },
  );
  out.text = rest.replace(/\s+/g, " ").trim();
  return out;
}

/** Builds the query string for GET /search; null when nothing to search. */
export function searchParams(f: SearchFilters, spaceId?: string, before?: string): string | null {
  if (!f.text && !f.authorId && !f.channelId && !f.hasFile) return null;
  const p = new URLSearchParams();
  if (f.text) p.set("q", f.text);
  if (f.authorId) p.set("authorId", f.authorId);
  if (f.channelId) p.set("channelId", f.channelId);
  else if (spaceId) p.set("spaceId", spaceId);
  if (f.hasFile) p.set("has", "file");
  if (before) p.set("before", before);
  return p.toString();
}
