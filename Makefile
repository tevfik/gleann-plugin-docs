BINARY_NAME=gleann-plugin-docs
BIN_DIR=bin
SRC_DIR=cmd/gleann-plugin-docs

# ONNX page parser (layout + OCR). PROFILE is latin (default, includes Turkish),
# chinese, or all for both.
PROFILE ?= latin
EMBED_DIR=internal/docparser/embedded

all: build

build-bitllm:
	@if [ ! -f ext/bitllm.c/CMakeLists.txt ]; then \
		echo "📦 Initializing ext/bitllm.c submodule..."; \
		git submodule update --init --recursive ext/bitllm.c; \
	fi
	@if [ ! -f ext/bitllm.c/build/libbitllm.a ]; then \
		echo "🔧 Building bitllm.c static library..."; \
		mkdir -p ext/bitllm.c/build && cd ext/bitllm.c/build && cmake .. -DCMAKE_BUILD_TYPE=Release && make -j$$(nproc) bitllm_static; \
	fi

build: build-bitllm
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./$(SRC_DIR)

test:
	go test -v ./internal/parser ./internal/httpserver ./internal/docparser

# Fetch the ONNX models into ~/.gleann/models/docparser (SHA-256 verified;
# files that are already valid are not downloaded again).
download-doc-models: build
	./$(BIN_DIR)/$(BINARY_NAME) models download --profile $(PROFILE)

verify-doc-models: build
	./$(BIN_DIR)/$(BINARY_NAME) models verify --profile $(PROFILE)

# Single binary with the models linked in (about 20 MB larger); it then needs no
# model download, only libonnxruntime.
build-embedded: build
	@mkdir -p $(EMBED_DIR) $(BIN_DIR)
	./$(BIN_DIR)/$(BINARY_NAME) models download --profile $(PROFILE) --dir $(EMBED_DIR)
	go build -tags embedmodels -o $(BIN_DIR)/$(BINARY_NAME)-embedded ./$(SRC_DIR)

# Accuracy and latency of the ONNX parser against a PDF's own text layer:
#   make bench-docparser PDF=paper.pdf PAGES=1-3
PAGES ?= 1
bench-docparser:
	@test -n "$(PDF)" || (echo "usage: make bench-docparser PDF=file.pdf [PAGES=1-3]"; exit 2)
	go run ./cmd/docparser-bench -pdf "$(PDF)" -pages $(PAGES) -dpi 200

# Document format extraction performance across sample documents:
#   make bench [DIR=testdata/documents] [ITER=5]
ITER ?= 5
DIR ?= testdata/documents
bench: build
	go run ./cmd/docs-bench -dir $(DIR) -iter $(ITER)

# Standard Go microbenchmarks for all format engines:
bench-engine:
	go test -bench=. -benchmem ./internal/engine

test-all:
	go test -v ./...

INSTALL_DIR  ?= /usr/local/bin
USER_BIN_DIR ?= $(HOME)/.local/bin

# register — add the plugin to ~/.gleann/plugins.json (uses the binary's own path)
register: build
	./$(BIN_DIR)/$(BINARY_NAME) --install

# install-user — build, install to ~/.local/bin (no sudo) and register
# (written to a temp name and renamed so a running server does not block it)
install-user: build
	@mkdir -p $(USER_BIN_DIR)
	install -m 0755 $(BIN_DIR)/$(BINARY_NAME) $(USER_BIN_DIR)/$(BINARY_NAME).new
	mv -f $(USER_BIN_DIR)/$(BINARY_NAME).new $(USER_BIN_DIR)/$(BINARY_NAME)
	$(USER_BIN_DIR)/$(BINARY_NAME) --install
	@echo "✅ Installed $(BINARY_NAME) → $(USER_BIN_DIR)/$(BINARY_NAME)"

# install — system-wide to /usr/local/bin (needs sudo) and register
install: build
	sudo install -m 0755 $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME).new
	sudo mv -f $(INSTALL_DIR)/$(BINARY_NAME).new $(INSTALL_DIR)/$(BINARY_NAME)
	$(INSTALL_DIR)/$(BINARY_NAME) --install
	@echo "✅ Installed $(BINARY_NAME) → $(INSTALL_DIR)/$(BINARY_NAME)"

# uninstall — remove the plugin registration and both binaries
uninstall:
	-$(BIN_DIR)/$(BINARY_NAME) uninstall
	rm -f $(USER_BIN_DIR)/$(BINARY_NAME)
	@if [ -f $(INSTALL_DIR)/$(BINARY_NAME) ]; then sudo rm -f $(INSTALL_DIR)/$(BINARY_NAME); fi

clean:
	rm -rf $(BIN_DIR)

.PHONY: all build test test-all register install install-user uninstall clean download-doc-models verify-doc-models build-embedded bench-docparser bench bench-engine
