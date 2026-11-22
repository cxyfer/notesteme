# syntax=docker/dockerfile:1
#
# Notesteme 的 Docker image（ADR-0009、plan 第 18 節）。
# build context 是 repo 根目錄：
#   docker build -f deploy/docker/Dockerfile -t notesteme .

ARG GO_VERSION=1.25
ARG DEBIAN_VERSION=trixie
ARG DENO_VERSION=2.9.8
# 與 web/client/.nvmrc、.github/workflows/web.yml 一致（Node LTS）。
ARG NODE_VERSION=24.12.0

# ---- 建置 Web UI（web/client → dist，ADR-0008）----
# 產物與平台無關：只在 build 平台上建置一次，不在 QEMU 裡跑 Node。
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-${DEBIAN_VERSION}-slim AS web
WORKDIR /src/web/client
COPY web/client/package.json web/client/package-lock.json web/client/.npmrc ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit
COPY web/client ./
RUN npm run build

# ---- 建置 Go binary ----
# 在 build 平台上交叉編譯，多架構建置時不必在 QEMU 裡跑 Go 編譯器。
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-${DEBIAN_VERSION} AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
# go:embed 的 web/client（Go 檔與建置產物）；沒有這一步，binary 只會有「UI 尚未建置」的 placeholder。
COPY web/client/*.go ./web/client/
COPY --from=web /src/web/client/dist ./web/client/dist

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# modernc.org/sqlite 是純 Go，關閉 cgo 產生靜態 binary。
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w -X github.com/cxyfer/notesteme/internal/app.version=${VERSION}" \
      -o /out/notesteme ./cmd/notesteme

# ---- Deno（yt-dlp 解 YouTube 需要外部 JS runtime）----
FROM denoland/deno:bin-${DENO_VERSION} AS deno

# ---- 執行環境 ----
FROM debian:${DEBIAN_VERSION}-slim

# 空字串表示建置時安裝最新版；要固定版本時傳入 --build-arg YTDLP_VERSION=2026.10.26。
ARG YTDLP_VERSION=

# tini 當 PID 1：轉送訊號，並回收 yt-dlp 等子程序留下的孤兒程序。
# curl 只給 HEALTHCHECK 使用。
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      ffmpeg \
      python3 \
      python3-venv \
      tini \
 && rm -rf /var/lib/apt/lists/*

# yt-dlp 裝在獨立的 venv（ADR-0009）；yt-dlp[default] 會一併安裝 yt-dlp-ejs。
RUN python3 -m venv /opt/yt-dlp \
 && /opt/yt-dlp/bin/pip install --no-cache-dir --upgrade pip \
 && /opt/yt-dlp/bin/pip install --no-cache-dir "yt-dlp[default]${YTDLP_VERSION:+==${YTDLP_VERSION}}" \
 && ln -s /opt/yt-dlp/bin/yt-dlp /usr/local/bin/yt-dlp

COPY --from=deno /deno /usr/local/bin/deno
COPY --from=build /out/notesteme /usr/local/bin/notesteme
# 內嵌的 OpenCC 字典是 Apache-2.0：發布物要附上 LICENSE 與 NOTICE（ADR-0018）。
COPY --from=build /src/internal/zhconv/opencc/LICENSE /usr/share/doc/notesteme/opencc/LICENSE
COPY --from=build /src/internal/zhconv/opencc/NOTICE.md /usr/share/doc/notesteme/opencc/NOTICE.md

# 以非 root 使用者執行；/data 必須是 0700（NFR-SEC-5）。
RUN groupadd --system --gid 10001 notesteme \
 && useradd --system --uid 10001 --gid notesteme --home-dir /data --shell /usr/sbin/nologin notesteme \
 && mkdir -p /data /config \
 && chown notesteme:notesteme /data /config \
 && chmod 0700 /data

# 預設只綁 loopback；容器內要開放給 port mapping。
ENV NOTESTEME_LISTEN=0.0.0.0:8080 \
    NOTESTEME_DATA=/data \
    NOTESTEME_CONFIG=/config/notesteme.yaml \
    HOME=/data \
    LANG=C.UTF-8

USER notesteme:notesteme
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080

# /readyz 確認 DB 可寫、外部工具可用（plan 18.1）。
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS -o /dev/null http://127.0.0.1:8080/readyz || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "notesteme"]
CMD ["serve"]
