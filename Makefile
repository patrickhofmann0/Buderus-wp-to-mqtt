BINARY  := bin/buderus2mqtt
IMAGE   := buderus2mqtt:dev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build run test lint fmt tidy docker clean

all: fmt lint test build

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/buderus2mqtt

# Lädt Variablen aus .env und startet das Programm lokal.
run:
	set -a && . ./.env && set +a && go run -ldflags="$(LDFLAGS)" ./cmd/buderus2mqtt

test:
	go test -race -cover ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

tidy:
	go mod tidy

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin
