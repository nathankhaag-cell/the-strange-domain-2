// electron-builder configuration for the desktop installers.
//
// Signing is optional. With no certificates (the default) the installers are
// unsigned; macOS builds are ad-hoc signed so Apple Silicon Macs will open
// them after the usual "unidentified developer" step. When the release
// workflow has these secrets, electron-builder picks them up from the
// environment on its own:
//   macOS:   CSC_LINK + CSC_KEY_PASSWORD (Developer ID certificate, .p12 as
//            base64), and for notarization APPLE_API_KEY + APPLE_API_KEY_ID +
//            APPLE_API_ISSUER (or APPLE_ID + APPLE_APP_SPECIFIC_PASSWORD +
//            APPLE_TEAM_ID)
//   Windows: WIN_CSC_LINK + WIN_CSC_KEY_PASSWORD (or CSC_LINK on a Windows
//            runner)
"use strict";

const macCert = !!process.env.CSC_LINK || !!process.env.CSC_NAME;

/** @type {import("electron-builder").Configuration} */
module.exports = {
  appId: "io.github.nathankhaagcell.strangedomain",
  productName: "The Strange Domain",
  directories: { output: "dist", buildResources: "build" },
  files: ["src/**/*", "build/icon.png", "package.json"],
  asar: true,
  artifactName: "The-Strange-Domain-${version}-${os}-${arch}.${ext}",
  publish: null,

  win: {
    target: [{ target: "nsis", arch: ["x64"] }],
  },
  nsis: {
    oneClick: false,
    perMachine: false,
    allowToChangeInstallationDirectory: true,
    artifactName: "The-Strange-Domain-${version}-windows-${arch}-setup.${ext}",
  },

  mac: {
    target: [{ target: "dmg", arch: ["x64", "arm64"] }],
    category: "public.app-category.social-networking",
    // Ad-hoc signing ("-") when there is no certificate; hardened runtime
    // would reject Electron's own frameworks under an ad-hoc signature.
    identity: macCert ? undefined : "-",
    hardenedRuntime: macCert,
  },
  dmg: {
    artifactName: "The-Strange-Domain-${version}-macos-${arch}.${ext}",
  },

  linux: {
    target: [
      { target: "AppImage", arch: ["x64", "arm64"] },
      { target: "deb", arch: ["x64", "arm64"] },
    ],
    category: "Network",
    synopsis: "End-to-end encrypted voice and data for family and friends",
    maintainer: "The Strange Domain",
    executableName: "the-strange-domain",
    syncDesktopName: true,
  },
};
