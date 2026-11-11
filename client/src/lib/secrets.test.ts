import { describe, expect, it } from "vitest";
import { findSecrets, isSecretFileName } from "./secrets";

// Fake keys are assembled at runtime so secret scanners never see a
// key-shaped literal in the repository.
const rnd = (n: number, chars = "aB3dE5fG7h") =>
  Array.from({ length: n }, (_, i) => chars[i % chars.length]).join("");
const fake = {
  anthropic: ["sk", "ant", "api03", rnd(40)].join("-"),
  openai: ["sk", "proj", rnd(40)].join("-"),
  github: ["gh", "p_", rnd(36)].join(""),
  githubPat: ["github", "pat", rnd(60, "aB3_dE5")].join("_"),
  aws: ["AK", "IA", rnd(16, "ABCDEFG234")].join(""),
  google: ["AI", "za", rnd(35)].join(""),
  slack: ["xo", "xb-", "1234567890-", rnd(20)].join(""),
  stripe: ["sk", "_live_", rnd(24)].join(""),
  hf: ["h", "f_", rnd(34)].join(""),
};

describe("findSecrets", () => {
  it.each([
    ["anthropic", fake.anthropic, "sk-ant-…"],
    ["openai", fake.openai, "sk-proj-…"],
    ["github", fake.github, "ghp_…"],
    ["github", fake.githubPat, "github_pat_…"],
    ["aws", fake.aws, "AKIA…"],
    ["google", fake.google, "AIza…"],
    ["slack", fake.slack, "xoxb-…"],
    ["stripe", fake.stripe, "sk_live_…"],
    ["huggingface", fake.hf, "hf_…"],
  ])("detects %s tokens and shows only the public prefix", (kind, token, hint) => {
    const hits = findSecrets(`here is my key ${token} ok`);
    expect(hits).toEqual([{ kind, hint }]);
    expect(hits[0]!.hint).not.toContain(token.slice(-8));
  });

  it("detects private keys", () => {
    const pem = ["-----BEGIN", "OPENSSH PRIVATE KEY-----"].join(" ");
    expect(findSecrets(`${pem}\nabc\n-----END OPENSSH PRIVATE KEY-----`)).toEqual([
      { kind: "privateKey", hint: pem },
    ]);
  });

  it("detects secret assignments (.env, export, JSON/YAML)", () => {
    const v = rnd(24);
    expect(findSecrets(`DB_PASSWORD=${v}`)).toEqual([{ kind: "assignment", hint: "DB_PASSWORD=…" }]);
    expect(findSecrets(`export MY_SERVICE_TOKEN="${v}"`)[0]?.hint).toBe("MY_SERVICE_TOKEN=…");
    expect(findSecrets(`{\n    "apiKey": "${v}"\n}`)[0]?.hint).toBe("apiKey=…");
    expect(findSecrets(`client_secret: ${v}`)[0]?.kind).toBe("assignment");
  });

  it("reports a known token once, not again as assignment", () => {
    expect(findSecrets(`OPENAI_API_KEY=${fake.openai}`)).toEqual([{ kind: "openai", hint: "sk-proj-…" }]);
  });

  it.each([
    "OPENAI_API_KEY=your-api-key-here",
    "API_KEY=<insert key>",
    "GITHUB_TOKEN=${{ secrets.GITHUB_TOKEN }}",
    "TOKEN=$TOKEN",
    "PASSWORD=xxxxxxxxxxxxxxxx",
    "SECRET_NAME=short1",
    "npm install sk-learn-wrapper-package-name",
    "const tokenizer = new Tokenizer();",
    "see https://example.com/sk-docs/getting-started-with-keys",
    "git commit -m 'fix: rotate api key handling'",
    "function_name_with_underscores_and_more_text",
  ])("ignores harmless text: %s", (s) => {
    expect(findSecrets(s)).toEqual([]);
  });

  it("caps the number of hits", () => {
    const many = Array.from({ length: 20 }, (_, i) => `K${i}_TOKEN=${rnd(20)}${i}`).join("\n");
    expect(findSecrets(many, 3)).toHaveLength(3);
  });

  it("stays fast on large adversarial input", () => {
    const big = `${"A".repeat(500_000)}\n${"sk-".repeat(50_000)}\n${"_".repeat(200_000)}=`;
    const t0 = performance.now();
    findSecrets(big);
    expect(performance.now() - t0).toBeLessThan(1000);
  });
});

describe("isSecretFileName", () => {
  it.each([
    ".env",
    ".env.local",
    ".env.production",
    "id_rsa",
    "id_ed25519",
    "server.pem",
    "cert.key",
    "store.p12",
    ".npmrc",
    "C:\\proj\\.env",
    "credentials.json",
  ])("flags %s", (n) => expect(isSecretFileName(n)).toBe(true));
  it.each([
    ".env.example",
    ".env.sample",
    ".env.template",
    "id_rsa.pub",
    "environment.ts",
    "keyboard.png",
    "README.md",
    "monkey.txt",
  ])("allows %s", (n) => expect(isSecretFileName(n)).toBe(false));
});
