BINARY   := bin/checkmate
PKG      := ./cmd/checkmate
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

export CHECKMATE_DB_PATH ?= ./data/checkmate.db

.PHONY: help build run test test-race lint tidy migrate-up migrate-down migrate-status clean

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Build the server binary
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: ## Run the API server
	go run -ldflags "$(LDFLAGS)" $(PKG) serve

test: ## Run tests
	go test ./...

test-race: ## Run tests with the race detector
	go test -race ./...

lint: ## Vet and format-check
	go vet ./...
	@test -z "$$(gofmt -l . )" || (echo "gofmt needed:"; gofmt -l .; exit 1)

tidy: ## Tidy modules
	go mod tidy

migrate-up: ## Apply pending migrations
	go run $(PKG) migrate up

migrate-down: ## Roll back the last migration
	go run $(PKG) migrate down

migrate-status: ## Show migration state
	go run $(PKG) migrate status

clean: ## Remove build output
	rm -rf bin
