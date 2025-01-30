# Build stage
FROM golang:1.23.4-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev git make sqlite sqlite-dev nodejs npm

# Set working directory
WORKDIR /app

# Set GOPATH and add to PATH using correct syntax
ENV GOPATH=/go \
    PATH=$GOPATH/bin:$PATH

# Copy go mod files first to get the correct templ version
COPY go.mod go.sum ./
RUN go mod download

# Install templ with the latest version
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy source code
COPY . .

# Use the Linux build target from the Makefile
RUN make build-linux && \
    BINARY_NAME=$(ls -t bin/workcraft-linux-amd64-* | head -n1) && \
    cp "$BINARY_NAME" bin/workcraft-app

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache sqlite sqlite-dev ca-certificates

# Set working directory
WORKDIR /app

# Copy the binary and necessary files
COPY --from=builder /app/bin/workcraft-app ./workcraft-app
COPY --from=builder /app/static ./static

# Create directory for SQLite database
RUN mkdir -p /app/data

# Expose port
EXPOSE 6112

# Run the application
CMD ["./workcraft-app"]
