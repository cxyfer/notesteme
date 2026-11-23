# Hub 的部署（Postgres＋S3）

> 依據：[ADR-0030](adr/0030-hub-deployment-and-storage.md)。Hub 是獨立的程式 `notesteme-hub`，正式環境用 Postgres 存資料、S3 相容儲存（MinIO、Cloudflare R2、AWS S3）存 bundle 中的檔案。SQLite＋本機檔案（預設的 `store.driver: sqlite`、`blobs.driver: fs`）是參考 backend，適合開發與小型自架。

## 1. 組成

`deploy/docker/compose.hub.yaml`（project name `notesteme-hub`，所有服務都在 `hub` profile，與 Client 的 `compose.yaml` 分開）：

| 服務 | 說明 |
| --- | --- |
| `hub-postgres` | `postgres:16`，資料在 volume `hub-postgres-data`；只在 internal network，不映射 port |
| `hub-minio` | MinIO（固定版本），資料在 volume `hub-minio-data`；只在 internal network |
| `hub-minio-init` | 一次性：建立 bucket（已存在時不動） |
| `hub-migrate` | 一次性：`notesteme-hub migrate`；多個實例同時執行時以 advisory lock 排隊 |
| `hub` | Hub 本身（`Dockerfile` 的 `notesteme-hub` target，內嵌 `web/hub`）；只映射到 `127.0.0.1:8090`，另接 `hub-egress` 以連 OAuth 供應商 |

對外的 HTTPS 由前面的反向代理負責（Caddy、nginx、Cloudflare Tunnel），反向代理指向 `127.0.0.1:8090`。`public_url` 必須是對外的 https 網址（OAuth 的 redirect_uri 由它組成）。

## 2. 第一次部署

```sh
cd deploy/docker
cp .env.hub.example .env.hub            # 填入密鑰（見下）
mkdir -p hub-config
cp hub-config.example.yaml hub-config/notesteme-hub.yaml   # 填入 public_url、oauth.*.client_id、admins
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub up -d --build
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub run --rm hub doctor
```

`.env.hub` 的每個密鑰都要是隨機值，compose 在缺少時直接失敗（不會用預設值啟動）：

| 變數 | 用途 | 產生方式 |
| --- | --- | --- |
| `NOTESTEME_HUB_MASTER_KEY` | 信封加密的主金鑰（下架申請的聯絡 email）。**不在資料庫備份內，遺失後無法解密，請另外保存** | `openssl rand -base64 32` |
| `NOTESTEME_HUB_SESSION_KEY` | 加密等待中登入的 PKCE verifier；更換只會讓進行中的登入失敗 | `openssl rand -base64 32` |
| `NOTESTEME_HUB_POSTGRES_PASSWORD` | Postgres 密碼；compose 組成 `NOTESTEME_HUB_STORE_DSN`，只用英數字避免 URL 跳脫 | `openssl rand -hex 24` |
| `NOTESTEME_HUB_S3_ACCESS_KEY_ID`、`NOTESTEME_HUB_S3_SECRET_ACCESS_KEY` | MinIO 的 root 帳號，同時是 Hub 的 S3 憑證 | `openssl rand -hex 24`（secret） |
| `NOTESTEME_HUB_OAUTH_<PROVIDER>_CLIENT_SECRET` | 有設定 `client_id` 的登入方式才需要 | 供應商後台 |

第一位管理員：寫在設定檔的 `admins`，或登入一次之後執行 `docker compose … run --rm hub admin grant --provider github --subject <id>`。

## 3. 設定（store、blobs）

- `store.driver: postgres`：DSN 來自環境變數 `NOTESTEME_HUB_STORE_DSN`（可含密碼），或設定檔的 `store.dsn`（**不能含密碼**，密碼改用 `PGPASSWORD` 或 `~/.pgpass`）。DSN 沒有指定時的預設值：連線池 10 條（`pool_max_conns`）、連線逾時 10 秒（`connect_timeout`）、`statement_timeout` 30 秒。錯誤訊息與 log 不含 DSN 的密碼。
- `blobs.driver: s3`：`blobs.s3.{endpoint, region, bucket, prefix, path_style, access_key_id, timeout}`，或環境變數 `NOTESTEME_HUB_S3_{ENDPOINT, REGION, BUCKET, PATH_STYLE, ACCESS_KEY_ID}`（環境變數優先）；`NOTESTEME_HUB_S3_SECRET_ACCESS_KEY` 只能來自環境變數。物件鍵是 `[<prefix>/]<sha256>`，相同內容只存一份；上傳前驗證 sha256 並附 Content-MD5，下載時檢查長度與 sha256（截斷或損壞的物件回錯誤）。單一物件上限同 `bundles.max_file_bytes`，`timeout`（預設 60s）涵蓋單一請求。
- 改用 Cloudflare R2：`endpoint: https://<account-id>.r2.cloudflarestorage.com`、`region: auto`、`path_style: false`，並從 compose 移除 `hub-minio`、`hub-minio-init` 與 `NOTESTEME_HUB_S3_ENDPOINT`。AWS S3：不設 endpoint，`region` 填 bucket 所在區域。
- Postgres 與 S3 是 owner 設定的連線，不經過 netguard（它們通常在私有網路上）。

## 4. 升級

```sh
git pull
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub up -d --build   # hub-migrate 會先套用新的 migration
```

降版執行（資料庫的 schema 比程式新）時 Hub 拒絕啟動。升級前先做一次備份。

## 5. 備份與還原

需要備份三樣東西：Postgres、物件儲存，以及 `.env.hub`（特別是主金鑰）。

```sh
# Postgres（邏輯備份；Hub 可以繼續執行）
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub exec -T hub-postgres \
  pg_dump -U notesteme_hub -d notesteme_hub -Fc > hub-$(date +%F).dump

# MinIO：bucket 的內容以 sha256 為鍵、寫入後不再改變，可以增量同步到另一個位置（例如 mc mirror 到 R2，或備份 volume）。
docker run --rm -v notesteme-hub_hub-minio-data:/data:ro -v "$PWD":/backup debian:trixie-slim \
  tar -C /data -czf /backup/hub-minio-$(date +%F).tgz .
```

還原（新的主機）：

```sh
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub up -d hub-postgres hub-minio
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub exec -T hub-postgres \
  pg_restore -U notesteme_hub -d notesteme_hub --clean --if-exists < hub-YYYY-MM-DD.dump
# 物件：把 tgz 解回 hub-minio-data volume（或 mc mirror 回來），再啟動全部服務。
docker compose -f compose.hub.yaml --env-file .env.hub --profile hub up -d
```

順序上先備份 Postgres、再備份物件：資料庫引用的 blob 一定已經在物件儲存中（上傳時先寫 blob 再寫資料庫）；多出來沒有被引用的物件不影響正確性。

## 6. 開發與測試

- 驗收場景一律跑 SQLite＋fs（ADR-0030）。
- Postgres store 與 S3 backend 的單元測試由環境變數啟用，沒有設定時略過：
  - `NOTESTEME_TEST_POSTGRES_DSN=postgres://user:pw@127.0.0.1:5432/db?sslmode=disable`（URL 格式；每個測試建立自己的 schema，結束時刪除）
  - `NOTESTEME_TEST_S3_ENDPOINT`、`NOTESTEME_TEST_S3_BUCKET`、`NOTESTEME_TEST_S3_ACCESS_KEY_ID`、`NOTESTEME_TEST_S3_SECRET_ACCESS_KEY`（bucket 要先建立）
- CI 的 `hub-backends` job 以 service container 跑這兩組測試（`.github/workflows/ci.yml`）。
