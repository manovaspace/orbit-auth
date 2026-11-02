# Standalone image build (GitHub/GHCR). Shared modules are pinned in go.mod.
FROM golang:1.27.3-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /auth ./cmd/auth

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /auth /app/auth
EXPOSE 10100
USER nobody
CMD ["/app/auth"]
