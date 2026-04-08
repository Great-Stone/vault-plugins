PLUGIN_NAME ?= vault-plugin-secrets-machine
DIST_DIR ?= dist

.PHONY: build
build:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 go build -o $(DIST_DIR)/$(PLUGIN_NAME) ./cmd/$(PLUGIN_NAME)

.PHONY: build-linux
build-linux:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=$(shell go env GOARCH) CGO_ENABLED=0 go build -o $(DIST_DIR)/$(PLUGIN_NAME) ./cmd/$(PLUGIN_NAME)

.PHONY: test
test:
	go test ./...
