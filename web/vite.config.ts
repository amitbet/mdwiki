import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

const DEFAULT_BACKEND = "http://127.0.0.1:8080";

/** `??` keeps empty strings; empty MDWIKI_BACKEND breaks the proxy and yields confusing 404s. */
function backendURL(mode: string, env: Record<string, string>): string {
  const raw = process.env.MDWIKI_BACKEND || env.MDWIKI_BACKEND || "";
  const trimmed = raw.trim();
  if (trimmed.length > 0) {
    return trimmed;
  }
  return DEFAULT_BACKEND;
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const backend = backendURL(mode, env);
  const wsBackend = backend.replace(/^http/, "ws");

  const backendProxy = {
    "/api": { target: backend, changeOrigin: true },
    "/auth": { target: backend, changeOrigin: true },
    "/health": { target: backend, changeOrigin: true },
    "/ws": { target: wsBackend, ws: true, changeOrigin: true },
  };

  return {
    plugins: [react()],
    test: {
      environment: "jsdom",
      include: ["src/**/*.test.ts"],
    },
    server: {
      port: 5173,
      host: "localhost",
      proxy: backendProxy,
    },
    // `vite preview` does not use `server.proxy` unless mirrored here
    preview: {
      proxy: backendProxy,
    },
  };
});
