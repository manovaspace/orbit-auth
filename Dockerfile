FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates
ENV GOPROXY=direct
WORKDIR /src/orbit/orbit-auth
COPY orbit/orbit-notifications /src/orbit/orbit-notifications
COPY orbit/orbit-observability /src/orbit/orbit-observability
COPY orbit/orbit-auth/go.mod orbit/orbit-auth/go.sum ./
RUN go mod download
COPY orbit/orbit-auth/ .
RUN CGO_ENABLED=0 go build -o /auth ./cmd/auth

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /auth /app/auth
COPY orbit/orbit-auth/migrations /app/migrations
EXPOSE 10100
CMD ["/app/auth"]
