# Build stage
FROM --platform=$BUILDPLATFORM golang:1.23.4-alpine AS builder
# Install build dependencies
RUN apk add --no-cache gcc musl-dev git make sqlite sqlite-dev nodejs npm

# Set working directory
WORKDIR /app

# Set GOPATH and add to PATH
ENV GOPATH=/go \
    PATH=$GOPATH/bin:$PATH

# Copy go mod files first
COPY go.mod go.sum ./
RUN go mod download

# Install templ
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy source code
COPY . .

# Build for the target architecture
ARG TARGETARCH
RUN if [ "$TARGETARCH" = "amd64" ]; then \
    make build-linux && \
    cp $(ls -t bin/workcraft-linux-amd64-* | head -n1) bin/workcraft-app; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
    GOARCH=arm64 make build-linux && \
    cp $(ls -t bin/workcraft-linux-* | head -n1) bin/workcraft-app; \
    fi

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
