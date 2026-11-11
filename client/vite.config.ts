/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import pkg from "./package.json";
import { patchE2eeWorker } from "./src/lib/voice/e2eePpsPatch";

/** Browser build only: icon, no indexing, theme colours. */
function webHead(): Plugin {
  return {
    name: "lightspeak-web-head",
    transformIndexHtml: () => [
      { tag: "link", attrs: { rel: "icon", type: "image/svg+xml", href: "favicon.svg" }, injectTo: "head" },
      { tag: "meta", attrs: { name: "robots", content: "noindex" }, injectTo: "head" },
      { tag: "meta", attrs: { name: "color-scheme", content: "light dark" }, injectTo: "head" },
      { tag: "meta", attrs: { name: "referrer", content: "no-referrer" }, injectTo: "head" },
    ],
  };
}

/** H.264 + E2EE keyframe fix in livekit's worker bundle (src/lib/voice/e2eePpsPatch.ts). */
function e2eePpsFix(): Plugin {
  return {
    name: "lightspeak-e2ee-pps-fix",
    enforce: "pre",
    transform(code, id) {
      // The worker source itself: plain in the worker build, ?worker_file in dev
      // (not the ?worker wrapper the app imports).
      if (!/livekit-client[\\/]dist[\\/]livekit-client\.e2ee\.worker\.mjs(\?worker_file\b.*)?$/.test(id))
        return null;
      return { code: patchE2eeWorker(code), map: null };
    },
  };
}

// Tauri dev server must use a fixed port; src-tauri is not watched by Vite.
// `--mode web` builds the browser client that the server serves under /app/.
export default defineConfig(({ mode }) => {
  const web = mode === "web";
  return {
    plugins: [svelte(), e2eePpsFix(), ...(web ? [webHead()] : [])],
    worker: { format: "es" as const, plugins: () => [e2eePpsFix()] },
    clearScreen: false,
    base: web ? "/app/" : "/",
    publicDir: web ? "web-public" : false,
    define: { __APP_VERSION__: JSON.stringify(pkg.version) },
    server: {
      port: 1420,
      strictPort: true,
      host: "127.0.0.1",
      watch: { ignored: ["**/src-tauri/**"] },
    },
    build: {
      target: "es2023",
      outDir: web ? "dist-web" : "dist",
      emptyOutDir: true,
      cssCodeSplit: true,
      reportCompressedSize: true,
      chunkSizeWarningLimit: 600,
    },
    test: {
      include: ["src/**/*.test.ts"],
      environment: "node",
    },
  };
});
