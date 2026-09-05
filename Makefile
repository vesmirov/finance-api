VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: up down dev test lint build clean

up:            ## the whole stack in containers: web + api
	docker compose up -d --build --wait

down:
	docker compose down

dev:           ## fast iteration: API via go run, database in ./finance.db
	go run ./cmd/finance

test:
	go test ./...

lint:
	golangci-lint run

build:
	go build $(LDFLAGS) -o dist/finance ./cmd/finance

clean:
	rm -rf dist
