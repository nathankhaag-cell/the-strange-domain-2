# The Strange Domain

End-to-end encrypted voice and data for family and friends. Self-hosted on a home PC, a Raspberry Pi or a cloud server, and light enough to work over mesh networks, with LoRa (text and data) planned.

This repository holds the **Domain Node**, the server people's browsers, desktop apps and phone apps connect to. The full design lives in the project's `design/architecture-proposal.md`.

## Status

Phase 0, foundations. What exists today:
- A single Go binary that builds for Windows, macOS, Linux and Raspberry Pi (64-bit and 32-bit) with no C toolchain needed.
- A SQLite database with users, devices, domains, roles, members, bans, channels, invites and an audit log.
- Domain rules: anyone can create a domain and owns it; Abbots, Bishops and Wardens manage roles, invites, mute, kick and ban, and nobody can act on someone of equal or higher rank.
- Sign-in with no passwords: each device registers an Ed25519 public key and signs a single-use challenge to get a session token. The first account on a node becomes its administrator; everyone after that needs a Summons, which also joins them to that domain.
- Several devices per account: a signed-in device issues a single-use, ten-minute link code (shown as text or a QR code) that lets a new device register its own key under the same callsign. Devices can be listed and revoked. An old device can send its message history to a new one, encrypted for the new device's key, in chunks the node passes along and then deletes. A recovery key, derived on the device from a recovery code the person writes down, lets them reclaim their callsign on a new device if every device is lost; recovery removes the lost devices.
- The server side of end-to-end encryption: an MLS delivery service that stores and orders opaque messages, accepts one commit per epoch, hands out device KeyPackages, forwards Welcome messages, and pushes live events over a WebSocket (`/api/v1/stream`). The node never holds keys. Groups are domain channels or Conclaves (DMs outside a domain; a Confession has two members). Members with the delete permission can erase a message's ciphertext, leaving a tombstone.
- A JSON API for domains, Chapels and Voice Relays, Summons, roles and moderation (see `internal/server/api.go`), plus `/healthz` and `/api/v1/info`.
- The web client (`client/`, served by the node from `internal/server/web/`): enlist, reconnect, link a device, recover; domains, Chapels, Confessions and Conclaves; member list by role; Summons; role changes, Mute, Kick and Ban; devices and recovery code. Messages are encrypted in the browser with MLS ([ts-mls](https://github.com/LukaJCB/ts-mls)); the node only ever stores ciphertext. Everything, including the VT323 font, is bundled, so it works with no internet access.

Default roles in every new domain, highest first: **Abbot** (owner), **Bishop** (senior moderator), **Warden** (moderator), **Brother / Sister** (member, given to people who join by Summons) and **Postulant** (new or unverified). Owners can rename them.

Not built yet: voice, history transfer between devices, the Codex (audit log) view, and desktop and phone apps.

## Run it

```sh
go run ./cmd/domain-node -listen :8743
```

Then open http://localhost:8743. Data goes to your user config folder (`%AppData%\strange-domain` on Windows, `~/Library/Application Support/strange-domain` on macOS, `~/.config/strange-domain` on Linux) unless you pass `-data <dir>`.

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
internal/server     HTTP API and the embedded web client (built output in internal/server/web)
client/             web client sources (TypeScript, Preact, Vite)
deploy/             service files
```

## Test

```sh
go test ./...
cd client && npm run typecheck
```
