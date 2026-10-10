# The Strange Domain

End-to-end encrypted voice and data for family and friends. Self-hosted on a home PC, a Raspberry Pi or a cloud server, and light enough to work over mesh networks, with LoRa (text and data) planned.

This repository holds the **Domain Node**, the server people's browsers, desktop apps and phone apps connect to. The full design lives in the project's `design/architecture-proposal.md`.

## Status

Phase 0, foundations. What exists today:
- A single Go binary that builds for Windows, macOS, Linux and Raspberry Pi (64-bit and 32-bit) with no C toolchain needed.
- A SQLite database with users, devices, domains, roles, members, bans, channels, invites and an audit log.
- Domain rules: anyone can create a domain and owns it; Abbots and Bishops appoint roles; Abbots, Bishops and Wardens mute, kick, ban (timed or permanent) and delete messages; nobody can act on someone of equal or higher rank.
- Sign-in with no passwords: each device registers an Ed25519 public key and signs a single-use challenge to get a session token. The first account on a node becomes its administrator; everyone after that needs a Summons, which also joins them to that domain.
- Several devices per account: a signed-in device issues a single-use, ten-minute link code (shown as text or a QR code) that lets a new device register its own key under the same callsign. Devices can be listed and revoked. An old device can send its message history to a new one, encrypted for the new device's key, in chunks the node passes along and then deletes. A recovery key, derived on the device from a recovery code the person writes down, lets them reclaim their callsign on a new device if every device is lost; recovery removes the lost devices.
- The server side of end-to-end encryption: an MLS delivery service that stores and orders opaque messages, accepts one commit per epoch, hands out device KeyPackages, forwards Welcome messages, and pushes live events over a WebSocket (`/api/v1/stream`). The node never holds keys. Groups are domain channels or Conclaves (DMs outside a domain; a Confession has two members). Members with the delete permission can erase a message's ciphertext, leaving a tombstone.
- A JSON API for domains, Chapels and Voice Relays, Summons, roles and moderation (see `internal/server/api.go`), plus `/healthz` and `/api/v1/info`.
- The web client (`client/`, served by the node from `internal/server/web/`): enlist, reconnect, link a device, recover; domains, Chapels, Confessions and Conclaves; member list by role; Summons; role changes, Mute, Kick, timed Ban and a bans list; devices and recovery code. Messages are encrypted in the browser with MLS ([ts-mls](https://github.com/LukaJCB/ts-mls)); the node only ever stores ciphertext. Everything, including the VT323 font, is bundled, so it works with no internet access.
- Attachments (pictures, GIFs, PDFs, any file): each file is encrypted on the device with a fresh AES-256-GCM key; the node stores only the ciphertext on disk (`<data>/blobs`), and the key, hash, name and type travel inside the MLS message. PNG, JPEG, GIF and WebP show inline; everything else (SVG and HTML included) is download only. Size cap and per-person quota: `-max-upload-mb`, `-upload-quota-mb`.
- Profile pictures, cropped and re-encoded to a 256 px square on the device. These are **not** end-to-end encrypted: like callsigns, the node stores them and serves them to people who share a domain or Conclave.
- Unread counts, a short sound and OS notifications for new messages (no message text unless previews are turned on in Settings), with per-chat mute. On phones, the chat fills the screen and the domain list and member list are drawers.
- Voice and video calls, end-to-end encrypted: Voice Relays in each domain (join, leave, see who is in, mute, push to talk, key bindings for both, speaking indicator, camera and screen sharing with a zoomable large view) and calls in Confessions and Conclaves (ring, answer, decline, hang up). The node runs a small media relay (an SFU, `internal/rtc`, built on [pion/webrtc](https://github.com/pion/webrtc), pure Go) that forwards each person's media to the others over UDP (one port, 8745, by default; LAN addresses work with no internet). Every audio and video frame is encrypted in the client with AES-256-GCM (WebRTC encoded transforms) under a key exported from the chat's MLS group, which changes whenever the group's epoch does; the node only ever relays sealed frames. Browsers that cannot transform encoded frames refuse to join rather than send media unencrypted. Ports, NAT and options: [docs/INSTALL.md](docs/INSTALL.md#voice-and-video-calls).

Default roles in every new domain, highest first: **Abbot** (owner), **Bishop** (senior moderator), **Warden** (moderator), **Brother / Sister** (member, given to people who join by Summons) and **Postulant** (new or unverified). Owners can rename them.

What each role can do to people ranked below it (the node enforces this; nobody can act on someone of equal or higher rank, or give a role at or above their own):

| Role | Change roles | Mute, Kick, Ban | Delete others' messages |
| --- | --- | --- | --- |
| Abbot | Set anyone else to Bishop, Warden, Brother / Sister or Postulant | Yes | Yes |
| Bishop | Set people below Bishop to Warden, Brother / Sister or Postulant | Yes | Yes |
| Warden | No | Yes | Yes |
| Brother / Sister, Postulant | No | No | No (own messages only) |

Bans last 1 hour, 24 hours, 7 days, 30 days or permanently; a timed ban lifts itself when it runs out. A banned person leaves the domain like a kick (and its chats and calls) and cannot rejoin until the ban ends or is lifted. Wardens and above see the list of bans with the time left; a ban can be lifted early by anyone with the ban permission who outranks the person's role at the time of the ban (the Abbot can lift any ban). Everyone can delete their own messages; deleted messages show as "[ REDACTED ]" for everyone. Role changes, mutes, kicks, bans, unbans, expired bans and moderator deletes are recorded in the domain's audit log (the Codex), without message content, which the node cannot read. Domains created before this change get the ban permission for their Warden role when the node updates.

Desktop apps (Windows, macOS, Linux) and an Android app are thin shells around the same client: they ask for the node's address on first start. Releases with every download are built by GitHub Actions; see [docs/INSTALL.md](docs/INSTALL.md).

Not built yet: history transfer between devices, the Codex (audit log) view, and an iOS app.

## Run it

```sh
go run ./cmd/domain-node -listen :8743
```

Then open http://localhost:8743. Data goes to your user config folder (`%AppData%\strange-domain` on Windows, `~/Library/Application Support/strange-domain` on macOS, `~/.config/strange-domain` on Linux) unless you pass `-data <dir>`.

At start-up the node prints every address it can be opened at. It logs a one-line traffic `status` every minute (`-status-interval`, `0` turns it off), logs each request with `-log-level debug`, and checks GitHub for a newer release (`-update-check=false` turns that off; failures are silent). The first account on the node (its administrator) has a **Node admin** view in the client with live counters, backed by `GET /api/v1/admin/stats`.

**Calls** use UDP port 8745 for media (`-rtc-udp-port`; `-rtc-public-ip` behind NAT, `-rtc-video=false` for voice only, `-rtc=false` to turn calls off). See [docs/INSTALL.md](docs/INSTALL.md#voice-and-video-calls) for firewall and port-forwarding steps.

**Installing and releases:** [docs/INSTALL.md](docs/INSTALL.md) covers downloading the apps, running the node on a Raspberry Pi, and cutting a release (push a `v*` tag, or Actions > Release > Run workflow).

**HTTPS.** Browsers only give the client WebCrypto on secure pages, so a browser on another machine needs HTTPS. `-tls-self-signed` makes a certificate once, keeps it in `<data>/tls` and logs its SHA-256 fingerprint; `-tls-cert`/`-tls-key` use your own. With `-tls-listen :8744` the node serves HTTPS there and keeps plain HTTP on `-listen` (the desktop and Android apps work over plain HTTP).

## Web client

The client is TypeScript with [Preact](https://preactjs.com) and [Vite](https://vite.dev), in `client/`. Its build goes into `internal/server/web/`, which is committed, so `go build` works without Node.

```sh
cd client
npm ci
npm run typecheck
npm run build        # writes ../internal/server/web; rebuild the node afterwards
```

After changing anything in `client/`, run `npm run build` and commit `internal/server/web/` with your change; CI fails if the committed build does not match the sources.

For live reloading, run the node on :8743 and `npm run dev` in `client/`; Vite proxies `/api` (including the WebSocket) to the node.

How the client handles keys and encryption:
- Each browser is one device. Its Ed25519 sign-in key is created with WebCrypto as a non-extractable key and kept in IndexedDB (browsers without WebCrypto Ed25519 fall back to a raw key in IndexedDB). The session token is kept in `localStorage`.
- A recovery code (20 characters) is turned into a recovery key on the device (PBKDF2-SHA256, salted with the callsign); only the public half goes to the node.
- Every Chapel and Conclave is an MLS group (suite `MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519`). Each device publishes KeyPackages; the first device to commit at epoch 0 creates the group; any member's device adds devices that belong in the group (`GET /api/v1/groups/{gid}/devices`) and removes people who left or devices that were revoked. A commit that loses the race gets 409 and is retried after applying the winner.
- MLS keys are single use, so decrypted messages and group state are kept in IndexedDB on each device. A device cannot read messages sent before it joined a group.

## Apps

- `desktop/`: Electron. It loads the client from the node the person picks (so the client always matches the node), treats that one http:// origin as secure so WebCrypto works, and trusts a self-signed node only by the fingerprint the person confirmed. The window has no system frame: the client (and the connect page) draw the title bar through a small preload API (`window.sdDesktop`), and the window remembers its size, position, maximized and full-screen state. `npm ci && npm start` to run it, `npm run dist` to build installers (electron-builder).
- `mobile/`: Capacitor (Android). It bundles the client, built with `npm run build:app` in `client/`, and runs it at http://localhost, which counts as secure; the client then calls the node cross-origin, which the node allows for the app origins only. `npm ci && npm run build:web && npx cap sync android`, then build `mobile/android` with Gradle or Android Studio.

`.github/workflows/release.yml` builds all of these, plus the node for every platform, on every `v*` tag.

## Build for every platform

```sh
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o dist/ ./cmd/domain-node   # Raspberry Pi (64-bit OS)
CGO_ENABLED=0 GOOS=linux   GOARCH=arm GOARM=7 go build -o dist/ ./cmd/domain-node  # Raspberry Pi (32-bit OS)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/ ./cmd/domain-node
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/ ./cmd/domain-node   # Apple Silicon
```

CI builds all of these on every pull request and attaches them as downloadable artifacts.

On Linux or a Pi, `deploy/domain-node.service` runs the node as a systemd service.

## Layout

```
cmd/domain-node     the node binary
internal/store      SQLite database and migrations
internal/perm       permissions and rank rules
internal/domains    domains, roles, invites, moderation, channels
internal/auth       accounts, device keys, sign-in and sessions
internal/relay      MLS delivery service, Conclaves and live push
internal/rtc        media relay (SFU) for voice and video calls
internal/server     HTTP API and the embedded web client (built output in internal/server/web)
internal/tlsutil    HTTPS: self-signed certificate and fingerprints
client/             web client sources (TypeScript, Preact, Vite)
desktop/            desktop app (Electron)
mobile/             Android app (Capacitor)
deploy/             service files
docs/               INSTALL.md
```

## Test

```sh
go test ./...
cd client && npm run typecheck
cd desktop && npm test
```
