# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGO and SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled
ENV CGO_ENABLED=1
ENV GOOS=linux
RUN go build -o server ./cmd/server/main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache sqlite-libs ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/server .

# Copy web assets
COPY web ./web

# Create directory for database
RUN mkdir -p /data

# Set environment variable for database path
ENV DB_PATH=/data/helper_app.db

# Expose port
EXPOSE 8080

# Run the application
CMD ["./server"]

