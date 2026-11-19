# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
ENV CGO_ENABLED=0
ENV GOOS=linux
RUN go build -o server ./cmd/server/main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/server .

# Copy web assets
COPY web ./web
COPY web2 ./web2
# Веб-версия приложения (Expo), раздаётся по /app — собирается во фронте scripts/build-for-backend.sh
COPY webapp ./webapp

# Expose port
EXPOSE 8080

# Run the application
CMD ["./server"]

