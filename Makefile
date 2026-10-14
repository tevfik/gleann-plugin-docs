BINARY_NAME=gleann-plugin-docs
BIN_DIR=bin
SRC_DIR=cmd/gleann-plugin-docs

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./$(SRC_DIR)

test:
	go test -v ./internal/parser ./internal/httpserver

test-all:
	go test -v ./...

install: build
	./$(BIN_DIR)/$(BINARY_NAME) --install

clean:
	rm -rf $(BIN_DIR)

.PHONY: all build test test-all install clean
