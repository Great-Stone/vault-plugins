BINARY_NAME=vault-plugin-secrets-github
OUT_DIR=bin

.PHONY: build test clean

build:
	mkdir -p $(OUT_DIR)
	go build -o $(OUT_DIR)/$(BINARY_NAME) ./cmd/$(BINARY_NAME)

test:
	go test -count=1 -race ./...

clean:
	rm -rf $(OUT_DIR)
