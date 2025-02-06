.PHONY: dev build-mac build-linux clean recreate_db clear_db build-css templ

# Version from git tag, or default to 'dev' if no tag exists
VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
# Build date
BUILD_DATE = $(shell date -u '+%Y-%m-%d_%H:%M:%S')
# Git commit hash
COMMIT_HASH = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Combine version info
VERSION_INFO = $(VERSION)-$(COMMIT_HASH)

# Build flags
BUILD_FLAGS = -ldflags "-X main.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE) -X main.CommitHash=$(COMMIT_HASH)"

# Generate TEMPL files
templ:
	@echo "Generating TEMPL files..."
	@templ generate

# Development commands
dev:
	@trap 'kill 0' EXIT; \
	air & \
	npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --watch & \
	wait

# Build CSS
build-css:
	@echo "Building CSS..."
	@npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --minify

# Build commands
build-mac: build-css templ
	@echo "Building for Mac... Version: $(VERSION_INFO)"
	@GOOS=darwin GOARCH=amd64 go build $(BUILD_FLAGS) -o bin/workcraft-mac-amd64-$(VERSION_INFO)
	@GOOS=darwin GOARCH=arm64 go build $(BUILD_FLAGS) -o bin/workcraft-mac-arm64-$(VERSION_INFO)

build-linux: build-css templ
	@echo "Building for Linux... Version: $(VERSION_INFO)"
	@GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o bin/workcraft-linux-amd64-$(VERSION_INFO)

# Build for all platforms
build-all: build-mac build-linux

clear_db:
	@sqlite3 ./workcraft.db "DELETE FROM peon; DELETE FROM bountyboard;"
	@echo "Database cleared"

# Cleanup
clean:
	@rm -rf bin/
	@echo "Cleaned build directory"

# Show version info
version:
	@echo "Version: $(VERSION)"
	@echo "Build Date: $(BUILD_DATE)"
	@echo "Commit Hash: $(COMMIT_HASH)"
