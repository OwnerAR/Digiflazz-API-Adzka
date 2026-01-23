.PHONY: help build build-windows build-linux build-darwin clean compress compress-windows compress-linux compress-darwin all test tidy

# Variables
BIN_NAME := digiflazz-api
OUT_DIR := build
CMD_DIR := cmd/server
GO_VERSION := 1.24.0

# Default target
.DEFAULT_GOAL := help

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tidy: ## Run go mod tidy
	@echo "Running go mod tidy..."
	@go mod tidy

test: ## Run tests
	@echo "Running tests..."
	@go test ./...

build-windows: ## Build for Windows amd64
	@echo "Building for Windows amd64..."
	@mkdir -p $(OUT_DIR)
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o $(OUT_DIR)/$(BIN_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@echo "✓ Built: $(OUT_DIR)/$(BIN_NAME)-windows-amd64.exe"

build-linux: ## Build for Linux amd64
	@echo "Building for Linux amd64..."
	@mkdir -p $(OUT_DIR)
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o $(OUT_DIR)/$(BIN_NAME)-linux-amd64 ./$(CMD_DIR)
	@echo "✓ Built: $(OUT_DIR)/$(BIN_NAME)-linux-amd64"

build-darwin: ## Build for Darwin (macOS) amd64
	@echo "Building for Darwin amd64..."
	@mkdir -p $(OUT_DIR)
	@CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w" -o $(OUT_DIR)/$(BIN_NAME)-darwin-amd64 ./$(CMD_DIR)
	@echo "✓ Built: $(OUT_DIR)/$(BIN_NAME)-darwin-amd64"

build-darwin-arm64: ## Build for Darwin (macOS) arm64
	@echo "Building for Darwin arm64..."
	@mkdir -p $(OUT_DIR)
	@CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o $(OUT_DIR)/$(BIN_NAME)-darwin-arm64 ./$(CMD_DIR)
	@echo "✓ Built: $(OUT_DIR)/$(BIN_NAME)-darwin-arm64"

compress-windows: build-windows ## Build and compress for Windows amd64
	@echo "Creating archive for Windows..."
	@cd $(OUT_DIR) && \
	if [ -f ../.env.example ]; then \
		cp ../.env.example .env.example && \
		zip -q $(BIN_NAME)-windows-amd64.zip $(BIN_NAME)-windows-amd64.exe .env.example && \
		rm -f .env.example && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-windows-amd64.zip (with .env.example)"; \
	else \
		zip -q $(BIN_NAME)-windows-amd64.zip $(BIN_NAME)-windows-amd64.exe && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-windows-amd64.zip"; \
	fi
	@if [ -f $(OUT_DIR)/$(BIN_NAME)-windows-amd64.zip ]; then \
		du -h $(OUT_DIR)/$(BIN_NAME)-windows-amd64.zip | cut -f1 | xargs -I {} echo "  Archive size: {}"; \
	fi

compress-linux: build-linux ## Build and compress for Linux amd64
	@echo "Creating archive for Linux..."
	@cd $(OUT_DIR) && \
	if [ -f ../.env.example ]; then \
		cp ../.env.example .env.example && \
		tar -czf $(BIN_NAME)-linux-amd64.tar.gz $(BIN_NAME)-linux-amd64 .env.example && \
		rm -f .env.example && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-linux-amd64.tar.gz (with .env.example)"; \
	else \
		tar -czf $(BIN_NAME)-linux-amd64.tar.gz $(BIN_NAME)-linux-amd64 && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-linux-amd64.tar.gz"; \
	fi
	@if [ -f $(OUT_DIR)/$(BIN_NAME)-linux-amd64.tar.gz ]; then \
		du -h $(OUT_DIR)/$(BIN_NAME)-linux-amd64.tar.gz | cut -f1 | xargs -I {} echo "  Archive size: {}"; \
	fi

compress-darwin: build-darwin ## Build and compress for Darwin (macOS) amd64
	@echo "Creating archive for Darwin..."
	@cd $(OUT_DIR) && \
	if [ -f ../.env.example ]; then \
		cp ../.env.example .env.example && \
		tar -czf $(BIN_NAME)-darwin-amd64.tar.gz $(BIN_NAME)-darwin-amd64 .env.example && \
		rm -f .env.example && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-darwin-amd64.tar.gz (with .env.example)"; \
	else \
		tar -czf $(BIN_NAME)-darwin-amd64.tar.gz $(BIN_NAME)-darwin-amd64 && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-darwin-amd64.tar.gz"; \
	fi
	@if [ -f $(OUT_DIR)/$(BIN_NAME)-darwin-amd64.tar.gz ]; then \
		du -h $(OUT_DIR)/$(BIN_NAME)-darwin-amd64.tar.gz | cut -f1 | xargs -I {} echo "  Archive size: {}"; \
	fi

compress-darwin-arm64: build-darwin-arm64 ## Build and compress for Darwin (macOS) arm64
	@echo "Creating archive for Darwin arm64..."
	@cd $(OUT_DIR) && \
	if [ -f ../.env.example ]; then \
		cp ../.env.example .env.example && \
		tar -czf $(BIN_NAME)-darwin-arm64.tar.gz $(BIN_NAME)-darwin-arm64 .env.example && \
		rm -f .env.example && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-darwin-arm64.tar.gz (with .env.example)"; \
	else \
		tar -czf $(BIN_NAME)-darwin-arm64.tar.gz $(BIN_NAME)-darwin-arm64 && \
		echo "✓ Created: $(OUT_DIR)/$(BIN_NAME)-darwin-arm64.tar.gz"; \
	fi
	@if [ -f $(OUT_DIR)/$(BIN_NAME)-darwin-arm64.tar.gz ]; then \
		du -h $(OUT_DIR)/$(BIN_NAME)-darwin-arm64.tar.gz | cut -f1 | xargs -I {} echo "  Archive size: {}"; \
	fi

build: build-windows ## Build for Windows (default)

compress: compress-windows ## Build and compress for Windows (default)

all: ## Build and compress for all platforms
	@echo "Building and compressing for all platforms..."
	@$(MAKE) compress-windows
	@$(MAKE) compress-linux
	@$(MAKE) compress-darwin
	@$(MAKE) compress-darwin-arm64
	@echo ""
	@echo "✓ All builds completed!"

clean: ## Clean build directory
	@echo "Cleaning build directory..."
	@rm -rf $(OUT_DIR)
	@echo "✓ Cleaned build directory"

