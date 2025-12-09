# Build Stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN go build -o bot ./cmd/bot

# Run Stage
FROM alpine:latest

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bot .
COPY --from=builder /app/configs ./configs

# Expose monitoring port
EXPOSE 8080

CMD ["./bot"]
