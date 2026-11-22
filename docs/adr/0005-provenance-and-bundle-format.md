# ADR-0005：產出記錄完整來源，並以 bundle 格式匯出

- 狀態：已採納（Hub 相關的細節由 [ADR-0031](0031-hub-identity-permissions-and-content.md)、[ADR-0032](0032-client-hub-protocol-and-privacy.md) 定案，2026-11-23，待 owner 追認）
- 日期：2026-11-11
- 修訂：2026-11-12，衍生圖移除 ASS、加入摘要與章節；新增 Agent 自述模型、`imported_from`、`speakers`、`availability` 與匯入規則

## 背景

- Hub 要保存每份字幕和筆記的轉錄模型、校對模型、語言，並能依這些欄位篩選。
- Hub 還要支援「用別人的原始字幕，在自己這邊重新生成」。
- 在 Hub 出現之前，也需要在不同實例之間手動搬移產出（見 ADR-0011）。

## 決策

### 產出之間的衍生關係

每個產出都記錄它的上游產出和來源紀錄，形成一張衍生圖：

```
transcript(source) → transcript(normalized) → transcript(segmented)
  → transcript(corrected) → transcript(translated, zh-Hant-TW)
                          └→ note ← frames(selected)、chapters
transcript(normalized) → summary、chapters
```

SRT、VTT、ASS 是取用時即時渲染的格式，不在衍生圖中。

`provenance_json` 範例：

```json
{
  "executor": "api",
  "provider": "openai",
  "model": "whisper-1",
  "prompt": { "id": "correct-default", "version": 3, "sha256": "…" },
  "params_sha256": "…",
  "timing": "word",
  "tool_versions": { "notesteme": "0.1.0", "yt-dlp": "2026.11.01" },
  "created_at": "2026-11-11T10:00:00Z"
}
```

- `executor` 為 `agent` 時，模型由 Agent 自行回報，無法驗證：改記為 `model_reported`，並加上 `"model_verified": false`。Hub 依模型篩選時，這類版本另外標示。
- 從 bundle 匯入的產出，來源紀錄加上 `imported_from`（來源 bundle 的 `created_by` 與 hash），不視為已驗證。
- 匯入外部編輯過的字幕時，`executor` 記為 `human`。

### Bundle 格式 v0

副檔名為 `.notesteme.zip`，結構如下：

```
manifest.json
artifacts/<artifact-id>.<ext>
```

```json
{
  "bundle_version": 0,
  "item": {
    "platform": "youtube",
    "external_id": "<video-id>",
    "part": null,
    "title": "…",
    "channel": { "external_id": "<channel-id>", "title": "…" },
    "published_at": "2026-11-01",
    "duration_s": 1935,
    "original_lang": "en",
    "availability": "public"
  },
  "artifacts": [
    {
      "id": "<uuidv7>",
      "kind": "transcript",
      "variant": "source",
      "lang": "en",
      "format": "notesteme-cues+json",
      "path": "artifacts/<uuidv7>.json",
      "sha256": "…",
      "parents": [],
      "provenance": { "…": "…" }
    }
  ],
  "created_by": { "instance_id": "<uuidv7>", "app_version": "0.1.0" },
  "license": "<SPDX id，例如 CC-BY-NC-SA-4.0>"
}
```

- 原始字幕和衍生字幕都用 `notesteme-cues+json`，保留 `Timing` 和 word 級資料。SRT、ASS 是匯出時另外產生的格式。
- `notesteme-cues+json` 的頂層預留 `speakers: [{id, label}]`，每個 cue 有可為空的 `speaker`。MVP 不填，之後加入說話者辨識時就不是不相容的變更。
- `availability` 來自 yt-dlp。不是 `public` 的影音（會員限定、私人、需要登入），以及用 cookie 取得的字幕，預設禁止上傳到 Hub。
- 筆記中引用的圖片放在 `artifacts/`。預設**不包含**媒體檔。
- 匯入時，用 `(platform, external_id, part)` 比對既有的影音，並用 `sha256` 去重。
- Bundle 是不可信的輸入，匯入時：
  - 拒絕絕對路徑、`..` 與 symlink。
  - 限制解壓後的總大小、檔案數與壓縮比，防止 zip bomb。
  - 逐檔驗證 `sha256`，manifest 用 JSON schema 驗證。
  - 筆記的 Markdown 在 UI 渲染時不允許原始 HTML。
- **修訂（2026-11-23，M4 規格展開，[BND 規格](../specs/BND-bundles.md) 待決問題 N2～N4，待 owner 追認）**：manifest 另有 `files`（每個檔案的路徑、`size`、`sha256`，與 zip 內容雙向一致）、`source_name`、`created_at`；上限的數值（上傳 64 MiB、解壓合計 256 MiB、2000 個項目、單檔 64 MiB、壓縮比 100 倍）與 hardlink、裝置檔的判斷方式以 BND 規格為準。

## 考慮過的選項

- **只存最終檔案（SRT、MD）**：無法依模型篩選，也無法從中間的產出重新生成。
- **只存一個大 JSON**：無法直接用一般工具打開字幕和筆記。

## 影響

- 好處：
  - Hub 的核心資料模型在 Client 階段就已經成形。
  - 在 Hub 出現之前就能用檔案分享。
- 代價：
  - 每個步驟都要正確寫入來源紀錄。這件事要寫進測試，沒有來源紀錄的產出視為錯誤。
- `bundle_version` 在 M5 前可以有不相容的變更，M5 之後必須向後相容。
