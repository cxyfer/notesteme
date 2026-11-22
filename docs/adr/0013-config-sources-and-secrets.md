# ADR-0013：設定來源分層，secrets 用信封加密

- 狀態：已採納
- 日期：2026-11-12
- 修訂：2026-11-23，`config export`／`import` 的範圍先限定為處理方案與預設處理方案（M2 S11a；範圍、格式與已知限制記在 SEC 規格的待決問題，待 owner 追認）

## 背景

- plan 中同時出現兩種設定：YAML 設定檔（`/config`、`asr.providers`），以及存在 DB、可以從 UI 修改的設定（處理方案、secrets）。兩者沒有說明哪個為準。
- 有些設定等於能在主機上執行命令，例如 `command` adapter 和 publish hook。如果能從 API 或 MCP 修改，拿到 token 就等於能遠端執行命令。
- API key、cookie 要加密存放。master key 要能輪替，遺失時的後果也要寫清楚。

## 決策

### 設定來源

| 來源 | 內容 | 修改方式 |
|---|---|---|
| 環境變數 | master key；也可以覆寫設定檔中的任一欄位 | 重新啟動 |
| 設定檔（`/config/notesteme.yaml`） | listen 位址、資料路徑、外部工具路徑、對外請求的 allowlist、`command` adapter、publish hook | 編輯檔案後重新啟動 |
| DB | 處理方案、prompt、訂閱、一般 provider（`openai-compatible`、`anthropic`、`gemini`、`mistral`）、ASS 樣式、筆記模板、詞彙表、預算、通知、secrets | UI、API、MCP（依 token scope） |

- 優先順序：環境變數 > 設定檔 > 內建預設值。
- DB 管理的設定不會出現在設定檔中，兩者沒有重疊，所以不會有「以哪邊為準」的問題。
- **會執行命令的設定只能寫在設定檔。** API 與 MCP 只能列出和選用，不能新增或修改。UI 顯示這些項目時，標示「在設定檔中定義」。
- `notesteme config export` 把 DB 中的設定（不含 secrets）匯出成 YAML，`notesteme config import` 匯入，方便做版本控制或搬到另一個實例。

### Secrets

- **信封加密**：
  - 資料金鑰隨機產生，用來加密每一筆 secret，使用 AEAD 演算法（例如 XChaCha20-Poly1305）。
  - master key 來自環境變數 `NOTESTEME_MASTER_KEY`，只用來加密資料金鑰。加密後的資料金鑰存在 `encryption_keys`。
- **輪替**：
  - `notesteme secrets rotate-key` 用新的 master key 重新加密資料金鑰，不需要重新加密每一筆 secret。
  - 懷疑資料金鑰外洩時，加上 `--rotate-data-key` 產生新的資料金鑰，並重新加密全部 secrets。
- **遺失 master key**：
  - API key、cookie、通知設定無法解密，要重新輸入。
  - 登入密碼與 token 只存 hash，不受影響。其他資料也都不受影響。
  - 備份不包含 master key，文件提醒要另外保存。
- **使用時**：
  - Secrets 不寫進 log，也不出現在命令列參數裡。
  - cookie 在執行時才寫成權限 `0600` 的暫存檔交給 yt-dlp，用完就刪除。
  - API key 儲存後只顯示末四碼，不能再次查看完整內容，只能更換。

## 考慮過的選項

- **全部以檔案為準**：UI 的修改寫回 YAML，方便版本控制。但要處理註解保留、同時寫入，以及檔案和 UI 不一致的問題。
- **全部存 DB**：實作最簡單，但會執行命令的設定也能從 API 修改。
- **master key 直接加密每一筆 secret**：輪替時要重新加密全部資料，中途失敗會留下新舊金鑰混用的狀態。

## 影響

- 好處：
  - 每個設定只有一個來源。
  - 就算 token 外洩，也無法在主機上執行命令。
  - 輪替 master key 只是一個小操作。
- 代價：
  - 新增 `command` adapter 或 hook 時，要改設定檔並重新啟動。
  - 要維護 `config export`／`import` 的格式。
