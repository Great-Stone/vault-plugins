PLUGIN_NAME ?= vault-plugin-auth-mac-passkey
DIST_DIR ?= dist

.PHONY: build test clean

build:
	mkdir -p $(DIST_DIR)
	go build -o $(DIST_DIR)/$(PLUGIN_NAME) ./cmd/$(PLUGIN_NAME)

test:
	go test -count=1 -race ./...

clean:
	rm -rf $(OUT_DIR)

