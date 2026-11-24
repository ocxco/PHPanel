import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import pkg from "./package.json";

export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: { strictPort: true, port: 5173 },
  // 版本号唯一来源：frontend/package.json
  define: { __APP_VERSION__: JSON.stringify(pkg.version) },
});
