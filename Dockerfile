# =============================================================================
# OpenClaw Dashboard — Multi-stage Dockerfile
# Supports both Go binary (default) and Python server
#
# Go (default, recommended):
#   docker build -t openclaw-dashboard .
#   docker run -p 8080:8080 -v ~/.openclaw:/home/dashboard/.openclaw openclaw-dashboard
#
# Python:
#   docker build --target python -t openclaw-dashboard:python .
#   docker run -p 8080:8080 -v ~/.openclaw:/home/dashboard/.openclaw openclaw-dashboard:python
# =============================================================================

# --- Stage 1: Build Go binary ---
FROM golang:1.26-alpine AS go-builder

WORKDIR /build
COPY go.mod main.go server.go chat.go config.go version.go index.html ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o openclaw-dashboard .

# --- Stage 2: Go runtime (default) ---
FROM alpine:3.21 AS go

RUN apk add --no-cache bash curl jq git

WORKDIR /app
COPY --from=go-builder /build/openclaw-dashboard .
COPY refresh.sh themes.json config.json ./
RUN chmod +x refresh.sh openclaw-dashboard

RUN adduser -D -u 1001 dashboard && \
    mkdir -p /home/dashboard/.openclaw && \
    chown -R dashboard:dashboard /app /home/dashboard
USER dashboard

VOLUME ["/home/dashboard/.openclaw"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -qO /dev/null http://localhost:8080/ || exit 1

CMD ["./openclaw-dashboard", "--bind", "0.0.0.0", "--port", "8080"]

# --- Stage 3: Python runtime (alternative) ---
FROM python:3.12-slim AS python

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash curl jq git \
    && rm -rf /var/lib/apt/lists/*

COPY index.html server.py refresh.sh themes.json config.json ./
RUN chmod +x refresh.sh

RUN useradd -r -u 1001 dashboard && \
    mkdir -p /home/dashboard/.openclaw && \
    chown -R dashboard:dashboard /app /home/dashboard
USER dashboard

VOLUME ["/home/dashboard/.openclaw"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/')" || exit 1

CMD ["python3", "server.py", "--bind", "0.0.0.0", "--port", "8080"]
