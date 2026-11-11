import { describe, expect, it } from "vitest";
import { charCount, fileExt, isPreviewable, MAX_PREVIEW_BYTES, snippetExt, snippetFile } from "./snippet";

describe("snippet", () => {
  it("counts code points like the server", () => {
    expect(charCount("abc")).toBe(3);
    expect(charCount("😀😀")).toBe(2); // 4 UTF-16 units
  });

  it("detects diffs and JSON, else text", () => {
    expect(snippetExt("diff --git a/x b/x\nindex 1..2\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b")).toBe("diff");
    expect(snippetExt("--- a/file.ts\n+++ b/file.ts\n@@ -3,4 +3,5 @@")).toBe("diff");
    expect(snippetExt('{\n  "a": [1, 2]\n}')).toBe("json");
    expect(snippetExt("[1, 2, 3]")).toBe("json");
    expect(snippetExt("{ not json }")).toBe("txt");
    expect(snippetExt("npm ERR! something\n--- not a diff")).toBe("txt");
  });

  it("builds a UTF-8 text file", async () => {
    const f = snippetFile("hello ä");
    expect(f.name).toBe("snippet.txt");
    expect(f.type).toBe("text/plain;charset=utf-8");
    expect(await f.text()).toBe("hello ä");
  });

  it("knows text extensions", () => {
    expect(fileExt("snippet.TXT")).toBe("txt");
    expect(fileExt("Dockerfile")).toBe("dockerfile");
    expect(fileExt("noext")).toBe("");
  });

  it("previews small text files only", () => {
    const a = { filename: "build.log", mime: "application/octet-stream", size: 1200 };
    expect(isPreviewable(a)).toBe(true);
    expect(isPreviewable({ ...a, size: MAX_PREVIEW_BYTES + 1 })).toBe(false);
    expect(isPreviewable({ ...a, size: 0 })).toBe(false);
    expect(isPreviewable({ filename: "photo.png", mime: "image/png", size: 1200 })).toBe(false);
    expect(isPreviewable({ filename: "data", mime: "text/plain", size: 10 })).toBe(true);
    expect(isPreviewable({ filename: "setup.exe", mime: "application/x-msdownload", size: 10 })).toBe(false);
  });
});
