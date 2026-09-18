import { resolve } from "path";
import { defineConfig, externalizeDepsPlugin } from "electron-vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  main: {
    build: { rollupOptions: { preserveEntrySignatures: "strict", external: ["electron"], input: { index: resolve("src/main/index.ts"), "normal-startup": resolve("src/main/normal-startup.ts") }, output: { strictExecutionOrder: true, hoistTransitiveImports: false, format: "cjs", entryFileNames: "[name].js" } } },
    // Workspace packages export TypeScript source, not Node-loadable bundles.
    plugins: [externalizeDepsPlugin({ exclude: ["@multica/core"] })],
  },
  preload: {
    // One CJS bundle keeps sandboxed preload imports local; window context limits exposed APIs.
    build: {
      lib: { entry: resolve("src/preload/index.ts"), formats: ["cjs"], fileName: () => "index.js" },
      rollupOptions: { external: ["electron"], output: { codeSplitting: false } },
    },
    plugins: [externalizeDepsPlugin({ exclude: ["@electron-toolkit/preload"] })],
  },
  renderer: {
    server: {
      // Allow parallel worktrees to run `pnpm dev:desktop` side-by-side
      // (e.g. Multica Canary alongside a primary checkout) by overriding
      // the renderer port via env. Falls back to 5173 for the common case.
      port: Number(process.env.DESKTOP_RENDERER_PORT) || 5173,
      strictPort: true,
    },
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        "@": resolve("src/renderer/src"),
      },
      dedupe: ["react", "react-dom", "@tanstack/react-query"],
    },
  },
});
