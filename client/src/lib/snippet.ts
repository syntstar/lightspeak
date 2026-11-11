// Long pastes become a text attachment instead of a message.
// The server's message limit stays; snippets go through the
// normal upload path with its size limit, quota and rate limit. Text files up
// to MAX_PREVIEW_BYTES can be viewed inline (on click, never automatically).
import type { Attachment } from "./api/types";

/** A pasted snippet larger than this should be sent as a real file. */
export const MAX_SNIPPET_BYTES = 1_000_000;
/** Text attachments up to this size can be opened inline. */
export const MAX_PREVIEW_BYTES = 512 * 1024;

/** Length as the server counts it (Unicode code points, not UTF-16 units). */
export function charCount(s: string): number {
  let n = 0;
  for (const _ of s) n++;
  return n;
}

export type SnippetExt = "diff" | "json" | "txt";

export function snippetExt(text: string): SnippetExt {
  const head = text.slice(0, 2000);
  if (/^(?:diff --git |--- \S[^\n]*\n\+\+\+ \S|@@ -\d)/m.test(head)) return "diff";
  const tr = text.trim();
  if ((tr.startsWith("{") && tr.endsWith("}")) || (tr.startsWith("[") && tr.endsWith("]"))) {
    try {
      JSON.parse(tr);
      return "json";
    } catch {
      /* plain text */
    }
  }
  return "txt";
}

const MIME: Record<SnippetExt, string> = {
  diff: "text/x-diff",
  json: "application/json",
  txt: "text/plain",
};

/** The pasted text as a file for the normal upload path. */
export function snippetFile(text: string): File {
  const ext = snippetExt(text);
  return new File([text], `snippet.${ext}`, { type: `${MIME[ext]};charset=utf-8` });
}

const TEXT_EXT = new Set(
  (
    "txt log md markdown diff patch json jsonc yaml yml toml ini cfg conf csv tsv xml html htm css scss " +
    "js mjs cjs jsx ts tsx mts svelte vue py go rs java kt kts c h cc cpp hpp cs sh bash zsh ps1 psm1 bat cmd " +
    "sql dockerfile gitignore gitattributes editorconfig env lock gradle swift rb php lua r tf"
  ).split(" "),
);

/** File extension in lower case ("" if none); "Dockerfile" counts as one. */
export function fileExt(name: string): string {
  const base = name.toLowerCase();
  if (base === "dockerfile") return "dockerfile";
  const dot = base.lastIndexOf(".");
  return dot >= 0 ? base.slice(dot + 1) : "";
}

/** Text attachment small enough to show inline. */
export function isPreviewable(a: Pick<Attachment, "filename" | "mime" | "size">): boolean {
  if (a.size <= 0 || a.size > MAX_PREVIEW_BYTES) return false;
  const mime = a.mime.toLowerCase();
  return TEXT_EXT.has(fileExt(a.filename)) || mime.startsWith("text/") || mime.startsWith("application/json");
}
