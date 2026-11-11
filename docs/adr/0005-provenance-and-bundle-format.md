# ADR-0005：產出記錄完整來源，並以 bundle 格式匯出

- 狀態：提議中（格式細節在 M5 前定案）
- 日期：2026-11-11

## 背景

- Hub 要保存每份字幕和筆記的轉錄模型、校對模型、語言，並能依這些欄位篩選。
- Hub 還要支援「用別人的原始字幕，在自己這邊重新生成」。
- 在 Hub 出現之前，也需要在不同實例之間手動搬移產出（見 ADR-0011）。

## 決策

### 產出之間的衍生關係

每個產出都記錄它的上游產出和來源紀錄，形成一張衍生圖：

```
transcript(source) → transcript(normalized) → transcript(segmented)
  → transcript(corrected) → transcript(translated, zh-Hant-TW) → ass
                          └→ note ← frames(selected)
```

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
    "original_lang": "en"
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
- 筆記中引用的圖片放在 `artifacts/`。預設**不包含**媒體檔。
- 匯入時，用 `(platform, external_id, part)` 比對既有的影音，並用 `sha256` 去重。

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
