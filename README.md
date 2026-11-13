# Notesteme

[![CI](https://github.com/cxyfer/notesteme/actions/workflows/ci.yml/badge.svg)](https://github.com/cxyfer/notesteme/actions/workflows/ci.yml)

Notesteme 把影音（YouTube、Bilibili、Podcast、本機檔案）轉成字幕與筆記。流程分成兩段：

- **確定性 workflow**：下載、取得字幕、轉錄、輸出。不需要 Agent，可以排程、重試、續跑。
- **LLM 步驟**：斷句、校正、翻譯、選圖、寫筆記。可以用內建 API 執行，也可以透過 MCP 交給 Agent。

完整的目標、範圍與里程碑見 [`docs/plan.md`](docs/plan.md)。

> [!NOTE]
> 專案仍在 **M1（擷取核心）** 開發中。目前可用的只有 `notesteme serve`（HTTP API、單一密碼登入、任務佇列）與 `notesteme --version`；
> CLI 的其他子命令、Web UI、MCP 尚未實作，未實作的 API 會回應 `501`。

## 快速開始（Docker）

Image 發布在 GHCR，支援 `linux/amd64` 與 `linux/arm64`，內含 ffmpeg（含 libass）、yt-dlp 與 Deno。

### Docker Compose

```bash
cd deploy/docker
cp .env.example .env
# 編輯 .env：填入 NOTESTEME_MASTER_KEY（可用 openssl rand -base64 32 產生）與 NOTESTEME_PASSWORD
docker compose up -d
curl -fsS http://127.0.0.1:8080/readyz
```

要從原始碼建置 image，改用 `docker compose up -d --build`。

### docker run

```bash
docker run -d --name notesteme \
  -e NOTESTEME_MASTER_KEY="$(openssl rand -base64 32)" \
  -e NOTESTEME_PASSWORD='change-me' \
  -v notesteme-data:/data \
  -p 127.0.0.1:8080:8080 \
  ghcr.io/cxyfer/notesteme:latest
```

> [!IMPORTANT]
> 請另外保存 `NOTESTEME_MASTER_KEY`。它用來加密 DB 中的資料金鑰，不在備份內；之後每次啟動都必須提供同一把。

### Image tag

| Tag | 來源 |
|---|---|
| `latest`、`master` | `master` 分支最新的 commit |
| `X.Y.Z`、`X.Y` | `vX.Y.Z` tag |
| `sha-<短 sha>` | 對應的 commit |

### 自行建置

```bash
docker build -f deploy/docker/Dockerfile -t notesteme .
deploy/docker/smoke-test.sh notesteme   # 冒煙測試
```

可用的 build arg：

| Build arg | 預設 | 說明 |
|---|---|---|
| `VERSION` | `dev` | `notesteme --version` 顯示的版本 |
| `GO_VERSION` | `1.25` | 建置用的 Go 版本 |
| `DENO_VERSION` | `2.9.8` | Deno 版本 |
| `YTDLP_VERSION` | 空（最新版） | 固定 yt-dlp 版本，例如 `2026.10.26` |

## 設定

設定的優先順序：**環境變數 > 設定檔 > 內建預設值**（[ADR-0013](docs/adr/0013-config-sources-and-secrets.md)）。

### 環境變數

| 變數 | 預設（原生 binary） | Image 內的值 | 說明 |
|---|---|---|---|
| `NOTESTEME_MASTER_KEY` | 無，**必填** | — | 加密資料金鑰的 master key。32 bytes 的 base64 直接作為金鑰，其他字串以 argon2id 推導 |
| `NOTESTEME_PASSWORD` | 無 | — | 登入密碼。只在 DB 還沒有密碼時（第一次啟動）寫入，之後會被忽略 |
| `NOTESTEME_LISTEN` | `127.0.0.1:8080` | `0.0.0.0:8080` | HTTP 監聽位址 |
| `NOTESTEME_DATA` | `/data` | `/data` | 資料目錄（DB、鎖檔、暫存），權限固定為 `0700` |
| `NOTESTEME_CONFIG` | `/config/notesteme.yaml` | `/config/notesteme.yaml` | 設定檔路徑；檔案不存在時視為沒有設定檔 |
| `NOTESTEME_YTDLP` | 無 | — | 指定 yt-dlp 的路徑 |
| `NOTESTEME_FFMPEG` | 無 | — | 指定 ffmpeg 的路徑 |

外部工具的尋找順序：設定檔 `tools` → 環境變數 → `PATH`。

### 設定檔

設定檔只放基礎設定；處理方案、prompt 等由 DB 管理的設定寫在設定檔時會拒絕啟動。

```yaml
listen: 0.0.0.0:8080
data: /data
# 連線來自這些位址時，登入鎖定改看 X-Forwarded-For。
trusted_proxies:
  - 172.18.0.0/16
tools:
  yt-dlp: /opt/yt-dlp/bin/yt-dlp
  ffmpeg: /usr/bin/ffmpeg
```

### Volume

| 路徑 | 內容 |
|---|---|
| `/data` | DB、產出、媒體快取、暫存 |
| `/config` | 設定檔（選用，可唯讀掛載） |

容器以 UID／GID `10001` 執行；bind mount 主機目錄時，請讓該使用者可以寫入 `/data`。

## HTTP 端點

| 端點 | 說明 |
|---|---|
| `GET /healthz` | 程序存活 |
| `GET /readyz` | DB 可寫、yt-dlp 與 ffmpeg 可執行；Docker `HEALTHCHECK` 使用這個端點 |
| `POST /api/auth/login` | 以密碼登入，取得 session cookie |
| `GET /api/openapi.json`、`GET /api/openapi.yaml` | OpenAPI 3.1 文件（需要登入） |

除了登入之外，`/api` 與 `/mcp` 底下的端點都需要登入；以 session 發出的非 GET 請求還要帶 CSRF token。

## 開發

需求：Go 1.25 以上。執行 pipeline 需要 `yt-dlp`、`ffmpeg`、`deno`；測試會使用假工具，不需要安裝。

```bash
go build -o notesteme ./cmd/notesteme

# 本機啟動（預設只綁 127.0.0.1:8080）
NOTESTEME_DATA=/tmp/notesteme/data \
NOTESTEME_CONFIG=/tmp/notesteme/notesteme.yaml \
NOTESTEME_MASTER_KEY="$(openssl rand -base64 32)" \
NOTESTEME_PASSWORD=dev \
  ./notesteme serve
```

### 測試

```bash
go test -race ./...                       # 單元測試、驗收場景與規格檢查
go test ./features -godog.tags=@AUTH-R1   # 只跑特定 tag 的驗收場景
```

- 驗收場景是 [`features/`](features/) 下的 Gherkin 檔，由 [godog](https://github.com/cucumber/godog) 執行；預設只跑 `@M1`，並排除 `@manual`、`@pending-m0`、`@wip`、`@ui`。
- 規格與場景的寫法見 [`docs/specs/README.md`](docs/specs/README.md)，測試 harness 的設計見 [ADR-0015](docs/adr/0015-acceptance-test-harness.md)。
- 測試不會連到外部網路：繞過 `netguard` 的連線一律失敗。

### 資料庫查詢

查詢以 [sqlc](https://sqlc.dev/) 產生（`internal/store/sqlcdb`），schema 直接讀 goose 的 migration。修改 `internal/store/queries/` 或 `internal/store/migrations/` 後：

```bash
cd internal/store && sqlc generate
```

## CI/CD

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) 在 PR、push 到 `master` 與 `v*` tag 時執行：

1. **Go lint & test**：`gofmt`、`go mod tidy -diff`、`go vet`、`go test -race`。
2. **sqlc generated code**：`sqlc diff` 確認產生的程式碼沒有過期。
3. **Docker image**：建置 `linux/amd64` image 並執行 [`deploy/docker/smoke-test.sh`](deploy/docker/smoke-test.sh)，確認：
   - 外部工具齊全（yt-dlp、ffmpeg 含 libass、ffprobe、Deno），以非 root 執行。
   - 沒有 master key 時拒絕啟動。
   - `HEALTHCHECK` 轉為 healthy，`/healthz`、`/readyz` 正常，未登入時 API 回應 `401`，登入成功與失敗的行為正確。
   - 收到 `SIGTERM` 時正常關閉，重新啟動後沿用 `/data` 中的密碼。

   push 到 `master` 或 `v*` tag 時，測試通過後才把 `linux/amd64`、`linux/arm64` image 推到 `ghcr.io/cxyfer/notesteme`。PR 只建置與測試，不發布。

發布新版本：

```bash
git tag v0.1.0
git push origin v0.1.0
```

## 文件

| 文件 | 內容 |
|---|---|
| [`docs/plan.md`](docs/plan.md) | 計畫：目標、範圍、架構、里程碑 |
| [`docs/adr/`](docs/adr/README.md) | 架構決策紀錄 |
| [`docs/specs/`](docs/specs/README.md) | 能力規格、故事地圖、步驟詞彙、非功能需求 |
| [`docs/ui/`](docs/ui/README.md) | UI 規格與設計語言 |

## 專案結構

```
cmd/notesteme/     程式入口
internal/          各模組（api、app、auth、pipeline、queue、store、source、subtitle 等）
features/          Gherkin 驗收場景與 step definitions
deploy/docker/     Dockerfile、compose、冒煙測試
docs/              計畫、ADR、規格、UI
```
