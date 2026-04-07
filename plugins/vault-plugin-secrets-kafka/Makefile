PLUGIN_NAME ?= vault-plugin-secrets-kafka
DIST_DIR ?= dist

# Native binary for the machine that runs Vault (e.g. macOS/Windows/Linux host).
# Using a Linux/amd64 binary here causes: fork/exec ... exec format error.
.PHONY: build
build:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 go build -o $(DIST_DIR)/$(PLUGIN_NAME) ./cmd/$(PLUGIN_NAME)

# Linux plugin for Docker (or remote Linux). GOARCH matches `go env GOARCH` so it
# aligns with typical Docker Desktop (arm64 on Apple Silicon, amd64 on Intel).
.PHONY: build-linux
build-linux:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=$(shell go env GOARCH) CGO_ENABLED=0 go build -o $(DIST_DIR)/$(PLUGIN_NAME) ./cmd/$(PLUGIN_NAME)

.PHONY: test
test:
	go test ./...

