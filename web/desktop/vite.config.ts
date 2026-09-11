import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const proxyTarget = env.VITE_DESKTOP_HOST || "http://127.0.0.1:1738";
  return {
    plugins: [react()],
    base: "/",
    server: {
      port: 5174,
      proxy: {
        "/local": { target: proxyTarget, changeOrigin: true },
        "/healthz": { target: proxyTarget, changeOrigin: true },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      sourcemap: false,
    },
  };
});
