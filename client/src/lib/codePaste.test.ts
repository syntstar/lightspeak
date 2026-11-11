import { describe, expect, it } from "vitest";
import { codeFence } from "./codePaste";

describe("codeFence", () => {
  it("fences shell commands", () => {
    expect(codeFence("curl -fsSL https://example.com/install.sh | bash")).toEqual({
      text: "```bash\ncurl -fsSL https://example.com/install.sh | bash\n```",
      lang: "bash",
    });
    expect(codeFence("npm install\nnpm run dev\n")?.lang).toBe("bash");
    expect(codeFence("$ git status")?.lang).toBe("bash");
  });

  it("detects PowerShell", () => {
    expect(codeFence("iex (irm https://example.com/install.ps1)")?.lang).toBe("powershell");
    expect(codeFence("Get-ChildItem C:\\\r\nSet-Location ..")?.text).toBe(
      "```powershell\nGet-ChildItem C:\\\nSet-Location ..\n```",
    );
  });

  it("fences multi-line code", () => {
    const ts = "function f(x) {\n  const y = x + 1;\n  return y;\n}";
    expect(codeFence(ts)?.text).toBe("```\n" + ts + "\n```");
    expect(codeFence("def f(x):\n    return x\n\nimport os")).not.toBeNull();
  });

  it("leaves prose, links and existing fences alone", () => {
    expect(codeFence("moin, wie gehts?")).toBeNull();
    expect(codeFence("go home, it is late")).toBeNull();
    expect(codeFence("https://example.com/a")).toBeNull();
    expect(codeFence("```js\nx()\n```")).toBeNull();
    expect(codeFence("Erste Zeile.\nZweite Zeile.\nDritte Zeile.")).toBeNull();
    expect(codeFence("set the table please")).toBeNull();
    expect(codeFence("make it so")).toBeNull();
    expect(codeFence("cd ./client")).not.toBeNull();
    expect(codeFence("go test ./...")).not.toBeNull();
    expect(codeFence("")).toBeNull();
  });
});
