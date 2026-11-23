# Notesteme

[![CI](https://github.com/cxyfer/notesteme/actions/workflows/ci.yml/badge.svg)](https://github.com/cxyfer/notesteme/actions/workflows/ci.yml)

Notesteme 把影音（YouTube、Bilibili、Podcast、本機檔案）轉成字幕與筆記。流程分成兩段：

- **確定性 workflow**：下載、取得字幕、轉錄、輸出。不需要 Agent，可以排程、重試、續跑。
- **LLM 步驟**：斷句、校正、翻譯、選圖、寫筆記。可以用內建 API 執行，也可以透過 MCP 交給 Agent。

完整的目標、範圍與里程碑見 [`docs/plan.md`](docs/plan.md)。

> [!NOTE]
> **M1（擷取核心）後端已完成**：HTTP API、CLI、任務佇列、字幕來源（YouTube、Bilibili、Podcast、本機檔案）、輸出 SRT／VTT／ASS／TXT 與匯入編輯過的字幕。
> **M2 進行中**：中文字形轉換（OpenCC）與輸出渲染已完成；LLM 步驟（斷句、校正、翻譯）與 MCP 仍在開發，**Web UI 尚未開始**。
> 未實作的 API 會回應 `501`。進度與已知限制見 [`docs/handoff.md`](docs/handoff.md)。

## CLI

同一個 binary 提供 server 與 CLI；`ingest`、`items` 等子命令依資料目錄的鎖自動選模式：沒有 server 持有資料目錄時由 CLI 自己執行（獨立模式），否則經由 server 的本機 socket；也可用 `--server`、`--token` 連到遠端 server。

```bash
notesteme serve                          # 啟動 server
notesteme ingest <url|ref|path> --wait   # 新增影音並等到完成
notesteme items ls                       # 列出影音
notesteme export <item> --format srt --out out.srt
notesteme doctor                         # 檢查 yt-dlp、ffmpeg 等外部工具與設定
notesteme --help                         # 完整子命令與旗標
```

| 子命令 | 說明 |
|---|---|
| `serve` | 啟動 server |
| `ingest` | 新增影音（`--profile`、`--until`、`--wait`、`--timeout`） |
| `items` | `ls`、`show`、`rerun --from <stage>`、`rm`（`--purge`、`--delete-outputs`）、`restore` |
| `trash` | `ls`、`empty` |
| `export` | 輸出字幕：`srt`、`vtt`、`txt`、`ass`（`--lang`、`--variant`、`--style`、`--out`） |
| `import` | 匯入外部編輯過的字幕為 `edited` 版本 |
| `tokens` | `create`、`ls`、`revoke`，管理 API token |
| `secrets` | `rotate-key`、`reset` |
| `doctor` | 檢查外部工具與設定 |

結束碼：`0` 成功、`1` 執行錯誤、`2` 參數錯誤、`3` 需要使用者處理、`4` 等待中。

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
| `X.Y.Z` | `vX.Y.Z` 或 `vX.Y.Z-rc.N` tag（完整版本號） |
| `latest` | 已發布的最高正式版；`-rc.N` 與較舊版本的 tag 不會移動它 |
| `X.Y` | 該 minor 系列中已發布的最高正式版 |

> [!NOTE]
> image 只在推送 `v*` tag 時建置，合併到 `master` 不會產生 image。

### 自行建置

```bash
docker build -f deploy/docker/Dockerfile -t notesteme .
deploy/docker/smoke-test.sh notesteme   # 冒煙測試
```

Hub（`notesteme-hub`，Postgres＋S3 相容儲存）的部署是另一份 compose（`deploy/docker/compose.hub.yaml`，`hub` profile），步驟、備份與還原見 [docs/hub-deployment.md](docs/hub-deployment.md)。

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

### CI

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) 在 PR 與 push 到 `master` 時執行，不建置 image：

1. **Go lint & test**：`gofmt`、`go mod tidy -diff`、`go vet`、`go test -race`。
   同一個 job 也用 [`scripts/ci/coverage.sh`](scripts/ci/coverage.sh) 檢查 `subtitle`、`pipeline`、`queue`、`netguard` 的涵蓋率不低於 80%。
2. **sqlc generated code**：`sqlc diff` 確認產生的程式碼沒有過期。
3. **govulncheck**：掃描程式實際呼叫到的已知弱點。
4. **goreleaser build**：以 [`.goreleaser.yaml`](.goreleaser.yaml) 做 snapshot 建置（linux amd64／arm64、windows amd64、darwin arm64），並對 linux/amd64 產物做 `--version` 冒煙測試；不發布。

### Fuzz

[`.github/workflows/fuzz.yml`](.github/workflows/fuzz.yml) 每天（UTC）執行一次，也可手動觸發；由 [`scripts/ci/fuzz.sh`](scripts/ci/fuzz.sh) 把總預算（預設 10 分鐘）平均分給所有 `Fuzz*` 目標，失敗時上傳新產生的失敗輸入。本機可用 `FUZZ_TIME=15s scripts/ci/fuzz.sh` 快速跑一輪。

### 發布 image

[`.github/workflows/docker-publish.yml`](.github/workflows/docker-publish.yml) 只在推送 `v*` tag（含 `-rc.N`）時執行，PR 與合併都不會建置，以節省私人 repo 的 Actions 額度：

1. **Checks**：先跑與 CI 相同的 Go 與 sqlc 檢查，tag 指向沒通過檢查的 commit 時不會發布。
2. **Build**：建置 `linux/amd64`、`linux/arm64`，以 digest 推到 `ghcr.io/cxyfer/notesteme`，只帶這個 tag 專屬的暫存 tag `staging-<tag>`；版本號取自 tag。
3. **Smoke test**：對推上去的 digest 執行 [`deploy/docker/smoke-test.sh`](deploy/docker/smoke-test.sh)，確認：
   - 外部工具齊全（yt-dlp、ffmpeg 含 libass、ffprobe、Deno），以非 root 執行。
   - 沒有 master key 時拒絕啟動。
   - `HEALTHCHECK` 轉為 healthy，`/healthz`、`/readyz` 正常，未登入時 API 回應 `401`，登入成功與失敗的行為正確。
   - 收到 `SIGTERM` 時正常關閉，重新啟動後沿用 `/data` 中的密碼。
4. **Promote**：測試通過後才把 release tag 指向同一個 digest，發布的 tag 不會對應到沒驗證過的建置。`latest` 與 `X.Y` 每次都重新指向已發布的最高正式版：較舊的 tag 晚完成不會讓它們倒退，失敗或尚未發布的更高 tag 也不會讓它們停在舊版。

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
