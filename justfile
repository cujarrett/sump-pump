# List available recipes
default:
    @just --list

# Run all CI checks locally before pushing
ci: lint test build

# Verify go.mod is tidy and run golangci-lint
lint:
    go mod tidy -diff
    golangci-lint run

# Run tests with race detector
test:
    go test -race ./...

# Build both binaries
build:
    go build -o bin/bridge ./cmd/bridge
    go build -o bin/consumer ./cmd/consumer

# Run one binary locally - just run bridge
run BINARY:
    go run ./cmd/{{BINARY}}
