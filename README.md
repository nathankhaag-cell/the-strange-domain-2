# The Strange Domain

End-to-end encrypted voice and data for family and friends. Self-hosted on a home PC, a Raspberry Pi or a cloud server, and light enough to work over mesh networks, with LoRa (text and data) planned.

This repository holds the **Domain Node**, the server people's browsers, desktop apps and phone apps connect to. The full design lives in the project's `design/architecture-proposal.md`.

## Status

Phase 0, foundations. What exists today:
- A single Go binary that builds for Windows, macOS, Linux and Raspberry Pi (64-bit and 32-bit) with no C toolchain needed.
- A SQLite database with users, devices, domains, roles, members, bans, channels, invites and an audit log.
- Domain rules: anyone can create a domain and owns it; Abbots, Bishops and Wardens manage roles, invites, mute, kick and ban, and nobody can act on someone of equal or higher rank.
- An HTTP server with `/healthz`, `/api/v1/info` and a placeholder page.

Default roles in every new domain, highest first: **Abbot** (owner), **Bishop** (senior moderator), **Warden** (moderator), **Brother / Sister** (member, given to people who join by Summons) and **Postulant** (new or unverified). Owners can rename them.

Not built yet: accounts and sign-in, the encrypted messaging protocol (MLS), voice, and the themed client.

## Run it

```sh
go run ./cmd/domain-node -listen :8743
```

Then open http://localhost:8743. Data goes to your user config folder (`%AppData%\strange-domain` on Windows, `~/Library/Application Support/strange-domain` on macOS, `~/.config/strange-domain` on Linux) unless you pass `-data <dir>`.

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
internal/server     HTTP API and the embedded web client
deploy/             service files
```

## Test

```sh
go test ./...
```
