.PHONY: build test lint vet fmt tidy clean e2e

BINARY := mcp-gateway
PKG := github.com/ayushraj/mcp-gateway
BIN_DIR := bin

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

install: build
	install -m 0755 $(BIN_DIR)/$(BINARY) $${GOBIN:-$${GOPATH:-$$HOME/go}/bin}/$(BINARY)

test:
	go test -race -count=1 ./...

cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "install golangci-lint: https://golangci-lint.run"; exit 1; }
	golangci-lint run ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .
	go mod tidy

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

e2e:
	go test -race -count=1 -tags=e2e ./internal/daemon/...
