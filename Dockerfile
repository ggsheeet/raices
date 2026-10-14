FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go tool templ generate \
	&& go tool sqlc generate \
	&& CGO_ENABLED=0 GOOS=linux go build -tags '!dev' -ldflags="-w -s" -o /out/raices .

# Litestream (static-ish release tarball; amd64 uses the static build for Alpine).
FROM alpine:3.22 AS litestream
ARG TARGETARCH
RUN apk add --no-cache ca-certificates wget \
  && if [ "$TARGETARCH" = "arm64" ]; then \
       wget -qO- https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-arm64.tar.gz | tar -C /usr/local/bin -xz; \
     else \
       wget -qO- https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64.tar.gz | tar -C /usr/local/bin -xz; \
     fi \
  && litestream version

FROM golang:1.26-alpine AS dev
WORKDIR /app
RUN go install github.com/air-verse/air@v1.61.7
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV ENVIRONMENT=development
EXPOSE 3000
CMD ["air", "-c", ".air.toml"]

FROM alpine:3.22 AS production
RUN apk add --no-cache ca-certificates \
	&& adduser -D -H -u 10001 app
COPY --from=builder /out/raices /raices
COPY --from=litestream /usr/local/bin/litestream /usr/local/bin/litestream
COPY scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh \
	&& mkdir -p /data \
	&& chown app:app /data
USER app
ENV ENVIRONMENT=production
ENV SQLITE_PATH=/data/raices.db
EXPOSE 3000
ENTRYPOINT ["/entrypoint.sh"]
