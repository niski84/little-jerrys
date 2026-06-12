# Pi-parity container for Little Jerry's Automator.
#
# Base image: debian:bookworm-slim — matches Raspberry Pi OS Bookworm. The
# binary built in this image is what we'll cross-compile and ship to the Pi
# directly (no container on the Pi — that would cost performance for no gain
# on a single-purpose appliance). The container exists as a dev/test bed so
# Playwright can hit the same surface the Pi will expose.
#
# Multi-stage: build stage compiles the Go binary + Tailwind CSS, runtime
# stage carries only what the binary needs at runtime (mpv, ffmpeg, an X
# display via Xvfb, and x11vnc so we can VNC in to see what mpv is showing).

# ────────────────────────────────────────────────────────────────────────────
# Build stage — Go + Node + Templ
# ────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS build

ARG DEBIAN_FRONTEND=noninteractive
# Bookworm's apt nodejs is v18 — too old for Tailwind 4 (needs Node 20+).
# Pull from NodeSource so the oxide native module loads correctly.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Pin Go version. Bookworm's apt Go is too old for this project.
ARG GO_VERSION=1.25.0
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
        | tar -C /usr/local -xz
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH

WORKDIR /src

# Layer cache: deps first, then sources.
COPY go.mod go.sum ./
RUN go mod download
COPY package.json package-lock.json* ./
RUN if [ -f package.json ]; then npm install --silent --no-audit --no-fund; fi

COPY . .

# Templ generate → Tailwind CSS → Go build.
RUN go run github.com/a-h/templ/cmd/templ@latest generate -path ./internal/jerry/views \
    && npm run --silent build:css \
    && CGO_ENABLED=0 go build -o /out/little-jerrys ./cmd/little-jerrys


# ────────────────────────────────────────────────────────────────────────────
# Runtime stage — mpv + headless X + VNC
# ────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        ffmpeg \
        mpv \
        xvfb \
        x11vnc \
        x11-utils \
        fonts-dejavu-core \
        procps \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/jerry
COPY --from=build /out/little-jerrys /opt/jerry/little-jerrys
COPY scripts/container-boot.sh /opt/jerry/boot.sh
RUN chmod +x /opt/jerry/boot.sh

# Defaults — override via `-e` or compose env. JERRY_RESCUE_PASSWORD is set
# so a fresh container is recoverable without --reset-password.
ENV PORT=8089 \
    DISPLAY=:0 \
    JERRY_MEDIA_ROOT=/media/usb \
    JERRY_STATE_PATH=/var/lib/jerry/state.json \
    JERRY_MPV_SOCKET=/tmp/mpv-jerry.sock \
    JERRY_RESCUE_PASSWORD=jerry-rescue

EXPOSE 8089 5900

HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8089/api/health || exit 1

CMD ["/opt/jerry/boot.sh"]
