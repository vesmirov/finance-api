VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: up down dev test lint build release clean db-up

up:            ## the whole stack in containers: web + api + db
	docker compose up -d --build --wait

down:
	docker compose down

db-up:         ## DB only (for go run and tests)
	docker compose up -d --wait db

dev: db-up     ## fast iteration: DB in a container, API via go run
	go run ./cmd/finance

test:
	go test ./...

lint:
	golangci-lint run

build:
	go build $(LDFLAGS) -o dist/finance ./cmd/finance

release:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/finance-linux-arm64 ./cmd/finance

clean:
	rm -rf dist
