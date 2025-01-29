# Build stage
FROM golang:1.23.5-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev git make sqlite sqlite-dev nodejs npm

# Set working directory
WORKDIR /app

# Set GOPATH and add to PATH using correct syntax
ENV GOPATH=/go \
    PATH=$GOPATH/bin:$PATH

# Install templ
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Use the Linux build target from the Makefile
RUN make build-linux

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache sqlite sqlite-dev ca-certificates

# Set working directory
WORKDIR /app

# Copy the binary and necessary files
COPY --from=builder /app/bin/workcraft-linux-amd64 ./workcraft-app
COPY --from=builder /app/static ./static

# Create directory for SQLite database
RUN mkdir -p /app/data

# Expose port
EXPOSE 6112

# Run the application
CMD ["./workcraft-app"]
