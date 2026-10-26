# AGENTS.md

Go service (`cmd/buderus2mqtt`) that polls a Buderus heat pump gateway's local REST API (K 40 RF Local API, self-signed TLS on port 9443) and publishes JSON to MQTT plus Home Assistant discovery. Read-only against the gateway.

## Commands
- Go **1.27** and golangci-lint **v2** (CI pins v2.15.0; config `.golangci.yml` is v2 format, so v1 won't work).
- Verify like CI: `golangci-lint run` then `go test -race ./...` (`make all` = fmt, lint, test, build).
- Format with `golangci-lint fmt` (gofmt + goimports), not plain `gofmt`.
- Single test: `go test ./internal/config -run TestSlugify -v`.
- `make run` sources `.env` (real gateway host/token, gitignored; never commit or print it). Local broker: `docker compose -f docker-compose.dev.yml up -d mosquitto`.
- The Docker build runs `go test ./...`, so failing tests also break image builds.

## Architecture
- `cmd/buderus2mqtt/main.go` wires everything; `internal/config` (env vars), `internal/buderus` (HTTP client, JSON models, path constants), `internal/mqtt` (Paho client, HA discovery builder), `internal/app/poller.go` (fetch → payload → publish).
- `app.Fetcher`/`app.Publisher` are interfaces defined by the consumer; tests use handwritten fakes and `httptest.NewTLSServer`, no mocking libs.
- Adding a new measured value usually touches all of: `buderus/model.go` + `client.go` (path/fetch), `app/poller.go` (payload + discovery `Sensor`), `mqtt/discovery.go` if new HA fields are needed, matching `_test.go` files, and the README tables (topics, fields, HA sensors).
- Payload fields are pointers with `omitempty`: values the gateway doesn't return are omitted, not zeroed. Temperature sensor fault values (`open`/`short`) and implausible readings outside −50..150 °C omit only that field; the other temperature fields are still published. A room setpoint marked `off` is omitted.
- `buderus.yml` is the vendor OpenAPI spec (Apache-2.0, see `NOTICE`); use it as API reference, don't edit it. `scripts/probe-*.sh` query a real gateway using `.env` to check what a device supports.
- `docs/plan-erweiterung.md` is a future plan (e.g. `app/definitions.go`, group pollers) — not the current state.

## Conventions
- Code comments, docs, README, and commit messages are in **German**. Commit messages are short and lowercase (e.g. `außentemperatur`).
- The maintainer is a Java developer: existing comments sometimes explain Go idioms with Java comparisons; keep that style when it helps.
- Keep README tables (config vars, MQTT topics, HA sensors) and `.env.example` in sync with code changes.
- Version is injected via `-ldflags "-X main.version=..."`; releases are `vX.Y.Z` git tags (CI builds and pushes multi-arch images to GHCR).
