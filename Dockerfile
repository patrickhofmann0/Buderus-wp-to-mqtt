# Build-Stage: läuft auf der Architektur des Build-Rechners und kompiliert
# per Cross-Compiling für die Zielarchitektur (kein langsames QEMU nötig).
FROM --platform=$BUILDPLATFORM golang:1.27 AS build

WORKDIR /src

# Abhängigkeiten zuerst laden, damit dieser Layer gecacht wird.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go test ./...

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/buderus2mqtt ./cmd/buderus2mqtt

# Laufzeit-Stage: minimales Image ohne Shell, läuft als non-root.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/buderus2mqtt /buderus2mqtt

USER nonroot:nonroot
ENTRYPOINT ["/buderus2mqtt"]
