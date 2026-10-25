FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /buderus-wp-to-mqtt ./cmd/buderus-wp-to-mqtt

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /buderus-wp-to-mqtt /buderus-wp-to-mqtt
USER 65534:65534
ENTRYPOINT ["/buderus-wp-to-mqtt"]
