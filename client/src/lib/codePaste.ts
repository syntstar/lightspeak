// Pasted commands and code become a code block (with copy button) instead of
// plain text, so a `curl … | bash` line can be copied in one click by the
// reader. Pure heuristic, deliberately conservative: prose stays prose.

const SHELL =
  /^(?:\$\s+|>\s+|PS [^>]*>\s*)?(?:sudo|curl|wget|iex|irm|iwr|npm|npx|pnpm|yarn|bun|git|gh|docker|kubectl|apt|apt-get|brew|winget|choco|scoop|pip|pip3|pipx|python|python3|py|node|deno|cargo|rustup|go|dotnet|java|mvn|gradle|ssh|scp|rsync|chmod|chown|mkdir|rm|cp|mv|cd|ls|cat|echo|export|set|setx|source|systemctl|journalctl|powershell|pwsh|cmd|reg|netsh|ping|tracert|nslookup|make|cmake|terraform|ansible|claude|codex)(?:\s|$)/;
/** Commands that are rarely ordinary words; everything else needs an argument
 *  that looks technical (flag, path, URL, file, pipe) to count. */
const STRONG =
  /^(?:\$\s+|>\s+|PS [^>]*>\s*)?(?:sudo|curl|wget|iex|irm|iwr|npm|npx|pnpm|winget|choco|pip3?|pipx|docker|kubectl|systemctl|journalctl|cargo|rustup|dotnet|pwsh|apt-get)\b/;
const SIGNAL = /\s(?:-{1,2}[\w-]|\.{0,2}\/|~\/|\$|[\w-]+=|[\w.-]+\.[a-z0-9]{1,5}\b)|[|&;<>]|:\/\/|^\$\s/;
const PWSH = /^(?:[A-Z][a-z]+-[A-Z][A-Za-z]+\b|\$env:|iex\b|irm\b|iwr\b)/;
const CODE_LINE =
  /(?:[;{}]\s*$|^\s*(?:import|export|from|def|class|function|const|let|var|return|if|for|while|fn|pub|func|package|#include|using|public|private)\b|=>|^\s*(?:\/\/|#!))/;

export interface FencedPaste {
  text: string;
  lang: string;
}

/** Returns the paste wrapped as a fenced code block, or null for normal text. */
export function codeFence(raw: string): FencedPaste | null {
  const text = raw.replace(/\r\n?/g, "\n").replace(/^\n+|\s+$/g, "");
  if (!text || text.includes("```") || text.length > 3800) return null;
  const lines = text.split("\n").filter((l) => l.trim());
  if (!lines.length) return null;
  const first = lines[0]!.trim();
  // URLs alone, sentences and mentions stay plain.
  if (/^https?:\/\/\S+$/.test(text)) return null;
  const isShell = (l: string) => PWSH.test(l) || STRONG.test(l) || (SHELL.test(l) && SIGNAL.test(l));
  const shell = lines.filter((l) => isShell(l.trim())).length;
  const piped = /\|\s*(?:bash|sh|iex|sudo\b)|&&|\biex\s*\(/.test(text);
  if (shell === lines.length || (lines.length === 1 && piped && isShell(first))) {
    const ps = lines.some((l) => PWSH.test(l.trim()) || /\biex\s*\(/.test(l));
    return {
      text: "```" + (ps ? "powershell" : "bash") + "\n" + text + "\n```",
      lang: ps ? "powershell" : "bash",
    };
  }
  if (lines.length >= 3) {
    const code = lines.filter((l) => CODE_LINE.test(l)).length;
    const indented = lines.filter((l) => /^(?: {2,}|\t)\S/.test(l)).length;
    if (code / lines.length >= 0.5 || (code >= 2 && indented / lines.length >= 0.3)) {
      return { text: "```\n" + text + "\n```", lang: "" };
    }
  }
  return null;
}
