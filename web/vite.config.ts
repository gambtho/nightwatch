import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Dev topology: the Vite dev server is the public origin. The Go server
// applies a strict Origin policy (foreign Origin → 403 before the handler)
// and sets no CORS headers, so the API must be same-origin with the app.
// Run the server with NIGHTSHIFT_PUBLIC_BASE_URL=http://localhost:5173 and
// let Vite proxy the API and the magic-link interstitial through.
declare const process: { env: Record<string, string | undefined> };
const server = process.env.NIGHTSHIFT_SERVER_URL ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/v1": server,
      "/auth": server,
    },
  },
});
