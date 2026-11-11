// Local check for secrets before they leave the composer:
// API keys and tokens of common services, private keys, `NAME_TOKEN=value`
// lines and files like `.env`. Runs only when sending/pasting/attaching;
// matches are shown to the user and never logged or sent anywhere.
// Every pattern is linear: no nested quantifiers, bounded identifier parts.

export type SecretKind =
  | "anthropic"
  | "openai"
  | "github"
  | "aws"
  | "google"
  | "slack"
  | "stripe"
  | "huggingface"
  | "privateKey"
  | "assignment";

export interface SecretHit {
  kind: SecretKind;
  /** Short, harmless hint where the match is (prefix or variable name). */
  hint: string;
}

const TOKEN_RULES: [SecretKind, RegExp][] = [
  ["anthropic", /\bsk-ant-[A-Za-z0-9_-]{20,}/g],
  // OpenAI: sk-… / sk-proj-… with at least one digit (avoids package names).
  ["openai", /\bsk-(?!ant-)(?=[A-Za-z0-9_-]{0,200}\d)[A-Za-z0-9_-]{20,}/g],
  ["github", /\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,})/g],
  ["aws", /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/g],
  ["google", /\bAIza[0-9A-Za-z_-]{35}/g],
  ["slack", /\bxox[abprs]-[A-Za-z0-9-]{10,}/g],
  ["stripe", /\b[rs]k_live_[A-Za-z0-9]{20,}/g],
  ["huggingface", /\bhf_[A-Za-z0-9]{30,}/g],
  ["privateKey", /-----BEGIN (?:[A-Z0-9]{1,20} ){0,3}PRIVATE KEY-----/g],
];

// NAME=value / NAME: value at the start of a line (dotenv, YAML, shell export).
const ASSIGN_RE =
  /^[ \t]{0,32}(?:export[ \t]+)?["']?([A-Za-z][A-Za-z0-9_]{0,63})["']?[ \t]{0,4}[=:][ \t]{0,4}["']?([^\s"'#]{1,512})/gm;
const SECRET_NAME = /(?:API_?KEY|SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE_?KEY|ACCESS_?KEY|AUTH)/i;
const PLACEHOLDER =
  /^(?:x{3,}|\*{3,}|\.{3}|<|\$|%|\{|your|my[-_]|changeme|example|placeholder|dummy|redacted|todo|none|null|true|false)/i;

/** Finds likely secrets in text (at most `max` hits, first per kind/hint). */
export function findSecrets(text: string, max = 5): SecretHit[] {
  const hits: SecretHit[] = [];
  const seen = new Set<string>();
  const add = (kind: SecretKind, hint: string) => {
    const key = `${kind}:${hint}`;
    if (seen.has(key) || hits.length >= max) return;
    seen.add(key);
    hits.push({ kind, hint });
  };
  for (const [kind, re] of TOKEN_RULES) {
    for (const m of text.matchAll(re)) {
      add(kind, kind === "privateKey" ? m[0] : `${publicPrefix(m[0])}…`);
      if (hits.length >= max) return hits;
    }
  }
  for (const m of text.matchAll(ASSIGN_RE)) {
    const [, name, value] = m as unknown as [string, string, string];
    if (!SECRET_NAME.test(name) || !looksSecret(value)) continue;
    // Already reported as a known token type.
    if (hits.some((h) => h.kind !== "assignment" && value.startsWith(h.hint.slice(0, -1)))) continue;
    add("assignment", `${name}=…`);
    if (hits.length >= max) break;
  }
  return hits;
}

// Only the fixed, public prefix of a token is ever shown.
const PREFIX_RE =
  /^(?:sk-ant-|sk-proj-|sk-|github_pat_|gh[pousr]_|AKIA|ASIA|AIza|xox[abprs]-|[rs]k_live_|hf_)/;
function publicPrefix(token: string): string {
  return PREFIX_RE.exec(token)?.[0] ?? "";
}

function looksSecret(v: string): boolean {
  if (v.length < 12 || PLACEHOLDER.test(v)) return false;
  return /[A-Za-z]/.test(v) && /\d/.test(v);
}

const SECRET_FILES = [
  /^\.env(?:\.(?!example$|sample$|template$|dist$|defaults$)[\w.-]{1,40})?$/i,
  /^id_(?:rsa|dsa|ecdsa|ed25519)$/i,
  /\.(?:pem|key|p12|pfx|jks|keystore)$/i,
  /^\.(?:npmrc|pypirc|netrc|git-credentials)$/i,
  /^credentials(?:\.json)?$/i,
];

/** File names that usually hold credentials (.env, private keys …). */
export function isSecretFileName(name: string): boolean {
  const base = name.split(/[\\/]/).pop() ?? name;
  return SECRET_FILES.some((re) => re.test(base));
}
