import { defineConfig } from "vite";

// The build lands in the Go server's embedded folder so `go build` ships it.
// Everything is local: the node must work offline (mesh networks, a Pi with
// no internet), and its CSP allows only same-origin scripts, styles, fonts
// and connections.
export default defineConfig({
  esbuild: { jsx: "automatic", jsxImportSource: "preact" },
  build: {
    outDir: "../internal/server/web", // relative to this folder
    emptyOutDir: true,
    target: "es2022",
    // No data: URIs (CSP default-src 'self' would block inlined fonts).
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      onwarn(warning, warn) {
        // @hpke/common ships commented-out code with pure annotations.
        if (warning.code === "INVALID_ANNOTATION") return;
        warn(warning);
      },
      // ts-mls loads optional post-quantum / X448 modules on demand; this
      // client only uses the X25519 + Ed25519 suite, so leave them out.
      external: [
        "@hpke/ml-kem",
        "@hpke/dhkem-x448",
        "@hpke/hybridkem-x-wing",
        "@hpke/chacha20poly1305",
        "@noble/post-quantum",
        /^@noble\/post-quantum\//,
      ],
    },
  },
  server: {
    proxy: {
      "/api": { target: "http://localhost:8743", ws: true },
      "/healthz": "http://localhost:8743",
    },
  },
});
