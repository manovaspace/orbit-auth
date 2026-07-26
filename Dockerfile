# Standalone image build (GitHub/GHCR). Local monorepo replace dirs are stripped;
# public manovaspace modules are fetched from the module proxy (@main until semver tags).
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates
ENV GOPROXY=https://proxy.golang.org,direct
ENV GONOSUMDB=github.com/manovaspace/*
WORKDIR /src
COPY . .
RUN set -euo pipefail \
	&& sed -i '/^replace (/,/^)/d' go.mod \
	&& sed -i '/^replace /d' go.mod \
	&& go mod edit -droprequire=github.com/manovaspace/orbit-notifications \
	&& go mod edit -droprequire=github.com/manovaspace/orbit-observability \
	&& go get github.com/manovaspace/orbit-notifications@main \
	&& go get github.com/manovaspace/orbit-observability@main \
	&& go mod tidy \
	&& CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /auth ./cmd/auth

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /auth /app/auth
COPY --from=builder /src/migrations /app/migrations
EXPOSE 10100
USER nobody
CMD ["/app/auth"]
