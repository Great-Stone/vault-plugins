PLUGIN_NAME ?= vault-plugin-secrets-kafka
DIST_DIR ?= dist

.PHONY: build
build:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST_DIR)/$(PLUGIN_NAME) ./cmd/$(PLUGIN_NAME)

.PHONY: test
test:
	go test ./...

