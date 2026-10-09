import { defineConfig, type Plugin } from "vite";

// The build lands in the Go server's embedded folder so `go build` ships it.
// Everything is local: the node must work offline (mesh networks, a Pi with
// no internet), and its CSP allows only same-origin scripts, styles, fonts
// and connections.
//
// `vite build --mode app` (npm run build:app) builds the same client for the
// phone app instead, into ../mobile/www. It runs at http://localhost and
// talks to the node the person picks (see src/node.ts), so its CSP, set in a
// meta tag because nothing serves headers there, also allows connections to
// other hosts.
const APP_CSP =
  "default-src 'self'; connect-src 'self' http: https: ws: wss:; object-src 'none'; base-uri 'none'; form-action 'none'";

// Vite runs this file in Node; the client's tsconfig has no Node types.
declare const process: { env: Record<string, string | undefined> };

function appCsp(): Plugin {
  return {
    name: "app-csp",
    transformIndexHtml: (html) =>
      html.replace("<meta charset=\"utf-8\">", `<meta charset="utf-8">\n<meta http-equiv="Content-Security-Policy" content="${APP_CSP}">`),
  };
}

export default defineConfig(({ mode }) => ({
  esbuild: { jsx: "automatic", jsxImportSource: "preact" },
  // The phone app's version, for its update check (src/ui/AppUpdate.tsx).
  // The release workflow sets APP_VERSION; the node's build leaves it empty
  // so that build stays reproducible.
  define: { __APP_VERSION__: JSON.stringify(mode === "app" ? (process.env.APP_VERSION ?? "dev") : "") },
  plugins: mode === "app" ? [appCsp()] : [],
  build: {
    outDir: mode === "app" ? "../mobile/www" : "../internal/server/web", // relative to this folder
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
}));
