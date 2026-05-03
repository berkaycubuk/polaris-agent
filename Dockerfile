FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download || true
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=$VERSION" -o /out/polaris ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache bash ca-certificates curl git python3 \
    && adduser -D -u 1000 polaris \
    && mkdir -p /app/data /home/polaris \
    && chown -R polaris:polaris /app /home/polaris
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/
COPY --from=build /out/polaris /app/polaris
USER polaris
WORKDIR /app
VOLUME ["/app/data"]
EXPOSE 8080
ENV DATA_DIR=/app/data \
    HOME=/home/polaris \
    HTTP_ADDR=:8080 \
    UV_CACHE_DIR=/app/data/.uv-cache \
    UV_PYTHON_PREFERENCE=only-system
ENTRYPOINT ["/app/polaris"]
