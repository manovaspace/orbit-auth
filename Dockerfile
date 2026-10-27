# Standalone image build (GitHub/GHCR). Shared modules are pinned in go.mod.
FROM golang:1.26.10-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /auth ./cmd/auth

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /auth /app/auth
COPY --from=builder /src/migrations /app/migrations
EXPOSE 10100
USER nobody
CMD ["/app/auth"]
