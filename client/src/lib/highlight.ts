// Syntax highlighting for code blocks. Loaded lazily the first
// time a code block with a language tag is shown, so it costs nothing until
// then. highlight.js returns an HTML string; it is parsed in an inert
// document and rebuilt from text nodes and <span class> only, so even a bug
// in the highlighter cannot inject markup.
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import cpp from "highlight.js/lib/languages/cpp";
import csharp from "highlight.js/lib/languages/csharp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import powershell from "highlight.js/lib/languages/powershell";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import shell from "highlight.js/lib/languages/shell";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const languages = {
  bash,
  cpp,
  csharp,
  css,
  diff,
  dockerfile,
  go,
  ini,
  java,
  javascript,
  json,
  markdown,
  powershell,
  python,
  rust,
  shell,
  sql,
  typescript,
  xml,
  yaml,
};
for (const [name, lang] of Object.entries(languages)) hljs.registerLanguage(name, lang);
// Aliases highlight.js does not ship for these languages.
hljs.registerAliases(["svelte", "vue"], { languageName: "xml" });
hljs.registerAliases(["jsonc", "json5"], { languageName: "json" });
hljs.registerAliases(["env", "conf", "cfg"], { languageName: "ini" });
hljs.registerAliases(["cmd", "bat"], { languageName: "powershell" });

/** Larger code stays plain text (highlighting cost grows with size). */
export const MAX_HIGHLIGHT_CHARS = 100_000;

const CLASS_RE = /^[\w-]{1,40}$/;

/** Highlighted copy of `code` as DOM nodes, or null for unknown languages. */
export function highlight(code: string, lang: string): DocumentFragment | null {
  const name = lang.trim().toLowerCase();
  if (!name || code.length > MAX_HIGHLIGHT_CHARS || !hljs.getLanguage(name)) return null;
  let html: string;
  try {
    html = hljs.highlight(code, { language: name, ignoreIllegals: true }).value;
  } catch {
    return null;
  }
  const doc = new DOMParser().parseFromString(`<div>${html}</div>`, "text/html");
  const root = doc.body.firstElementChild;
  if (!root) return null;
  const out = document.createDocumentFragment();
  rebuild(root, out);
  // Never show something else than the code: fall back if text differs.
  return out.textContent === code ? out : null;
}

function rebuild(src: Node, out: Node): void {
  for (const n of Array.from(src.childNodes)) {
    if (n.nodeType === Node.TEXT_NODE) {
      out.appendChild(document.createTextNode(n.nodeValue ?? ""));
    } else if (n.nodeType === Node.ELEMENT_NODE && (n as Element).tagName === "SPAN") {
      const span = document.createElement("span");
      const cls = Array.from((n as Element).classList).filter((c) => CLASS_RE.test(c));
      if (cls.length) span.className = cls.join(" ");
      rebuild(n, span);
      out.appendChild(span);
    } else {
      out.appendChild(document.createTextNode(n.textContent ?? ""));
    }
  }
}
