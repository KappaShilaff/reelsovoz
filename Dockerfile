FROM golang:1.25-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/application ./cmd/reelsovoz


FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
      python3 \
      python3-pip \
      wget && \
    python3 -m pip install --break-system-packages --no-cache-dir --upgrade yt-dlp && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --uid 10001 --create-home --home-dir /app appuser

ENV USER_STORAGE_FILE=/data/reelsovoz-users.json \
    YT_DLP_PATH=yt-dlp \
    FFMPEG_PATH=ffmpeg \
    DOWNLOAD_TIMEOUT=90s \
    PREPARE_TIMEOUT=10m \
    TELEGRAM_UPLOAD_RETRIES=3 \
    TELEGRAM_UPLOAD_TIMEOUT=120s \
    MAX_VIDEO_BYTES=100663296 \
    HEALTH_ADDR=:8000

COPY --from=builder /out/application /app/application
RUN chmod +x /app/application && \
    mkdir -p /data && \
    chown -R appuser:appuser /data && \
    chown -R appuser:appuser /app

USER appuser
WORKDIR /app
EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8000/healthz || exit 1
ENTRYPOINT ["/app/application"]
