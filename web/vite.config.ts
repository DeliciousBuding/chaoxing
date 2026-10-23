import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": "/src"
    }
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    sourcemap: false,
    target: "es2020",
    reportCompressedSize: false,
    modulePreload: { polyfill: false },
    chunkSizeWarningLimit: 620,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("/node_modules/")) return undefined;
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return "vendor-react";
          if (id.includes("/node_modules/lucide-react/") || id.includes("\\node_modules\\lucide-react\\")) return "vendor-icons";
          return "vendor";
        }
      }
    }
  }
});
