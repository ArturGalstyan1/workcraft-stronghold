.PHONY: dev build-mac build-linux clean recreate_db clear_db build-css

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
build-mac: build-css
	@echo "Building for Mac..."
	@GOOS=darwin GOARCH=amd64 go build -o bin/workcraft-mac-amd64
	@GOOS=darwin GOARCH=arm64 go build -o bin/workcraft-mac-arm64

build-linux: build-css
	@echo "Building for Linux..."
	@GOOS=linux GOARCH=amd64 go build -o bin/workcraft-linux-amd64

# Build for all platforms
build-all: build-mac build-linux

# Database commands
recreate_db:
	@rm -f workcraft.db
	@sqlite3 workcraft.db ".databases" ".quit"
	@echo "Database created"

clear_db:
	@sqlite3 ./workcraft.db "DELETE FROM peon; DELETE FROM bountyboard;"
	@echo "Database cleared"

# Cleanup
clean:
	@rm -rf bin/
	@echo "Cleaned build directory"
