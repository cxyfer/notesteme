# SEC：設定與 secrets

- 狀態：draft
- 里程碑：M1（`config export`／`import` 在 M2，和處理方案一起）
- 相關：plan 第 19 節；ADR-0013
- 驗收場景：[`features/SEC-config-secrets.feature`](../../features/SEC-config-secrets.feature)

## 目的

每個設定只有一個來源；會執行命令的設定碰不到 API；API key 與 cookie 加密存放，master key 可以輪替，遺失時的影響有限且明確。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| SEC-01 | As the owner, I want to know where each setting lives and which one wins, so that I am never surprised by the configuration in effect. | M1 |
| SEC-02 | As the owner, I want settings that run commands to be changeable only in the config file, so that a leaked token can never execute code on my machine. | M1 |
| SEC-03 | As the owner, I want API keys and cookies encrypted and never shown in full again, so that a copied database or a glance at the screen does not leak them. | M1 |
| SEC-04 | As the owner, I want to rotate the master key and recover from losing it, so that a key problem never costs me my library. | M1 |
| SEC-05 | As the owner, I want to export and import my settings as YAML, so that I can version them or move them to another instance. | M2 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| SEC-R1 | 設定的優先順序：環境變數 > 設定檔 > 內建預設值。環境變數用 `NOTESTEME_` 加上大寫的設定路徑，例如 `NOTESTEME_LISTEN`。 | M1 |
| SEC-R2 | 設定檔只放基礎設定：listen 位址、資料路徑、外部工具路徑、對外請求 allowlist、`command` adapter、publish hook。DB 管理的設定（處理方案、prompt、一般 provider、預算等）出現在設定檔時，啟動失敗並指出是哪個欄位。 | M1 |
| SEC-R3 | `command` 類型的 provider 與 hook 只能在設定檔定義。透過 API 新增或修改時回傳 403，訊息為「這個項目只能在設定檔中定義。」 | M1 |
| SEC-R4 | secrets 以資料金鑰加密，資料金鑰再由 master key（`NOTESTEME_MASTER_KEY`）加密。DB、log、備份中都不會出現 secret 的明文。 | M1 |
| SEC-R5 | 沒有設定 master key 時，`serve` 拒絕啟動，訊息為「尚未設定 NOTESTEME_MASTER_KEY。」master key 錯誤時拒絕啟動，訊息為「master key 不正確，無法解開資料金鑰。」並提示 `notesteme secrets reset`。 | M1 |
| SEC-R6 | API key 儲存後，API 與 UI 只回傳末四碼。 | M1 |
| SEC-R7 | cookie 在執行時才寫成權限 `0600` 的暫存檔交給 yt-dlp，子程序結束後立即刪除。 | M1 |
| SEC-R8 | `notesteme secrets rotate-key` 用新的 master key 重新加密資料金鑰；完成後舊的 master key 無法再啟動。 | M1 |
| SEC-R9 | `notesteme secrets reset` 刪除所有 secrets 與資料金鑰，並以新的 master key 建立新的資料金鑰；影音、產出、設定都保留，token 與密碼不受影響。 | M1 |
| SEC-R10 | `notesteme config export` 輸出 DB 管理的設定（不含 secrets）；`notesteme config import` 匯入時先驗證整份內容，有錯誤就整份不套用。 | M2 |

## 介面

- 設定檔：`/config/notesteme.yaml`
- 環境變數：`NOTESTEME_MASTER_KEY`、`NOTESTEME_<設定路徑>`
- API：`GET/PUT /api/settings/:section`、`POST /api/cookies`
- CLI：`notesteme secrets rotate-key|reset`、`notesteme config export|import`

## 不在範圍內

- 外部 secret manager（例如 Vault、1Password），目前沒有需求。

## 待決問題

- master key 的格式（32 bytes 的 base64，或允許任意密語再經 KDF）。暫定兩者都接受：長度不是 32 bytes 時用 argon2id 推導。
