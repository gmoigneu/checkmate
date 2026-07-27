# syntax=docker/dockerfile:1

FROM node:24-alpine AS web-builder

WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build


FROM golang:1.26.4-alpine AS server-builder

ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src/server

COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./
COPY --from=web-builder /src/server/internal/web/ui ./internal/web/ui

RUN CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/checkmate \
    ./cmd/checkmate


FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S checkmate \
    && adduser -S -G checkmate -h /app checkmate \
    && mkdir -p /data \
    && chown checkmate:checkmate /data

COPY --from=server-builder /out/checkmate /usr/local/bin/checkmate

ENV CHECKMATE_ENV=production \
    CHECKMATE_ADDR=:8080 \
    CHECKMATE_DB_PATH=/data/checkmate.db \
    CHECKMATE_AUTO_MIGRATE=true

USER checkmate
WORKDIR /app

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --quiet --spider http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/checkmate"]
CMD ["serve"]
