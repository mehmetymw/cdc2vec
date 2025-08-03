# Build stage
FROM golang:1.24.5-alpine AS builder

WORKDIR /app

# Install git for go modules
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cdc2vec ./cmd/cdc2vec

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/cdc2vec .

# Create directory for offset storage
RUN mkdir -p /data/offsets

# Expose health check port
EXPOSE 8080

# Run the binary
CMD ["./cdc2vec"]