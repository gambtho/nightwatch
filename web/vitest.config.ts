import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

// Paths are absolute because relative setupFiles resolution has proven
// unreliable when the working directory differs from the config directory.
const here = (p: string) => fileURLToPath(new URL(p, import.meta.url));

export default defineConfig({
  plugins: [react()],
  test: {
    root: here("."),
    environment: "jsdom",
    setupFiles: [here("./src/setupTests.ts")],
  },
});
