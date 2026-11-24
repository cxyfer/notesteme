# ADR-0031：Hub 的身分、權限與內容模型

- 狀態：提議中（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）
- 日期：2026-11-23
- 定案：[ADR-0012](0012-hub-access-control.md)（Hub 權限）與 [ADR-0005](0005-provenance-and-bundle-format.md) 中 Hub 相關的部分。兩份舊 ADR 的內容不改，只把狀態行改成「已採納（細節由 ADR-0031 定案）」。

## 背景

ADR-0012 定了權限的層次（站台可見度、群組、權限、單一內容的可見度）與自動規則的形狀，但沒有定：帳號連結的接管防護、PAT 與群組權限的關係、內建群組、下架的效果、投票與檢舉的濫用防護。ADR-0005 定了來源紀錄與 bundle，但 Hub 怎麼識別「同一份內容」、怎麼記錄衍生關係、`model_verified` 怎麼決定，留到 M5。

## 決策

### 1. 身分與帳號連結

- `users` 與 `identities` 一對多；識別鍵是 `(provider, subject)`：Google 的 `sub`、GitHub 的數字 `id`、Linux.do 的 `id`。使用者名稱與 email 只用來顯示與套用規則，不用來識別。
- **不依 email 自動合併帳號**：用新的登入方式第一次登入，一律建立新帳號（就算 email 和既有帳號相同）。理由：GitHub 與 Linux.do 的 email 不保證已驗證，依 email 合併等於讓任何能控制某個 email 的人接管帳號。
- 連結新的登入方式只能在已登入的狀態下發起，而且 session 的最近一次登入要在 10 分鐘內（HACC-R6）：被偷走的舊 session 不能拿來把攻擊者的身分連進來。
- 身分已經屬於另一個帳號時拒絕連結，不合併。合併帳號不在 M5 範圍內。

### 2. 權限與群組

| 權限 | 能做什麼 |
|---|---|
| `read` | 瀏覽、搜尋、看版本詳情與來源紀錄、投票、檢舉 |
| `download` | 下載渲染後的字幕與筆記（SRT、VTT、TXT、Markdown） |
| `regenerate` | 讓 Client 取得原始的 cues JSON（重新生成、ASR 前查詢） |
| `upload` | 分享 bundle、撤回自己的分享 |
| `moderate` | 處理檢舉與下架申請、隱藏與移除版本 |
| `admin` | 指派群組、停用帳號、看稽核紀錄；包含所有其他權限 |

- 內建群組（名稱固定，權限可以在設定檔調整）：
  - `members`：每個登入且沒有被停用的帳號，預設 `[read, download]`。
  - `contributors`：預設 `[read, download, regenerate, upload]`，成員來自自動規則或手動指派。
  - `moderators`：預設 `[read, download, moderate]`，只能手動指派。
  - `admins`：`[admin]`，只能由設定檔的 `admins` 清單或 `notesteme-hub admin grant` 指定，API 不能授予（避免權限提升）。
- 有效權限是帳號所屬群組權限的聯集。自動規則（ADR-0012 的形狀）在每次登入時重新評估，只影響 `source = rule` 的成員資格。
- PAT 的權限是建立時選定的子集；每個請求的有效權限是「PAT 的權限 ∩ 帳號當下的有效權限」，所以帳號失去群組後，PAT 也跟著失去權限。
- 匿名存取：站台可見度是 `authenticated`（預設）時，除了登入、健康檢查與下架申請，一律 401；是 `public` 時，匿名請求只有 `public` 內容的 `read`。

### 3. 內容模型

- **影片**：`(platform, external_id, part)`，與 Client 相同；`platform` 只接受 `youtube`、`bilibili`、`podcast`（本機檔案沒有公開的身分，不能分享）。
- **版本**：一個影片的一份產出內容，鍵是 `(video, kind, variant, lang, sha256)`。`sha256` 是產出檔案內容的雜湊（與 Client 的 `artifacts.sha256` 相同）。相同的鍵只存一份；另一個人上傳相同內容時，建立新的分享紀錄並連到既有的版本（去重），版本的「上傳者」清單多一人。
- **blob**：以 sha256 為鍵的檔案，多個版本（例如不同影片引用同一張圖）共用。
- **分享**：一次上傳。記錄上傳者、授權、聲明時間與包含的版本。撤回分享不刪除被其他分享引用的版本。
- **衍生關係**：
  - `parent`：版本的來源紀錄中 `parents_sha256` 的每一項，在同一部影片中有對應版本時記錄（例如 `corrected` 的 parent 是 `normalized`）。
  - `regenerated_from`：來源紀錄有 `hub_origin.version_id` 時記錄，代表「以 Hub 上某個版本的原始字幕重新生成」；版本詳情顯示「以 @<上傳者> 的原始字幕重新生成」。
  - 衍生關係在上傳時建立；parent 之後才上傳時，在那次上傳補上連結。
- **模型與 `model_verified`**：Hub 從來源紀錄取出三個篩選欄位：轉錄模型（`source` 這條線的 provider／model）、校正模型、翻譯模型。來源紀錄中只要有一個 `executor: agent`、`model_verified: false`，或來自 Hub 匯入（`executor: hub`）而原本未驗證的步驟，版本就是 `model_verified: false`。Hub 不能驗證任何 Client 回報的模型，`true` 的意思只是「由 Client 的 `api` executor 記錄」，Hub UI 的文案要寫清楚。
- **狀態**：`visible`、`hidden`（檢舉達門檻或被管理員隱藏，等待處理）、`removed`（下架或管理員移除，內容刪除）、`withdrawn`（所有分享都被撤回）。

### 4. 下架與濫用防護

- 下架申請不需要帳號（版權人不一定有帳號），以 IP 限流；聯絡 email 用信封加密，只有 `moderate` 看得到。
- 下架決定為 `remove` 時：版本變成 `removed`，沒有其他版本引用的 blob 立即刪除，`(video, sha256)` 記入 `removed_hashes`，之後相同內容再上傳時拒絕。
- 投票與檢舉：帳號建立滿 24 小時才能投票與檢舉；不能對自己上傳的版本投票；每人每版本一票；分數只計算沒有被停用的帳號；檢舉有每日上限，同一人對同一版本只能有一筆未處理的檢舉；3 個帳號齡滿 7 天的不同檢舉者就自動隱藏，等待管理員處理。數字可以在設定檔調整。

### 5. 資料表方向（`internal/hub/hubstore`，goose 從 00001 起）

```
users            (id, display_name, created_at, suspended_at, suspended_reason)
identities       (id, user_id, provider, subject, login, email, email_verified, attrs_json, linked_at, last_login_at)
                  -- UNIQUE (provider, subject)；attrs_json: trust_level、orgs
oauth_states     (state_hash, provider, intent, user_id, pkce_verifier_ct, nonce, created_at, expires_at, used_at)
                  -- intent: login | link；verifier 以 session 金鑰加密
sessions         (id, user_id, token_hash, csrf_hash, created_at, authenticated_at, expires_at, ip, user_agent)
pats             (id, user_id, name, permissions, token_hash, created_at, expires_at, last_used_at, revoked_at)
groups           (id, name, permissions, builtin)
group_members    (group_id, user_id, source, added_at)       -- source: manual | rule | config
videos           (id, platform, external_id, part, title, channel_external_id, channel_title,
                  duration_s, original_lang, published_at, first_shared_at)
                  -- UNIQUE (platform, external_id, part)
blobs            (sha256, size, media_type, created_at)
versions         (id, video_id, kind, variant, lang, format, sha256, size, provenance_json,
                  transcribe_model, correct_model, translate_model, model_verified,
                  visibility, license, state, score, created_at)
                  -- UNIQUE (video_id, kind, variant, lang, sha256)
version_blobs    (version_id, sha256, role)                  -- role: content | image
shares           (id, user_id, video_id, license, attested_at, bundle_sha256, created_at, withdrawn_at)
share_versions   (share_id, version_id, deduplicated)
derivations      (child_version_id, parent_version_id, relation)   -- relation: parent | regenerated_from
votes            (user_id, version_id, value, created_at, updated_at)  -- PRIMARY KEY (user_id, version_id)
reports          (id, version_id, reporter_id, reason, note, state, created_at, decided_by, decided_at)
takedowns        (id, version_id, video_id, requester_user_id, contact_email_ct, reason, statement,
                  state, created_at, decided_by, decided_at, decision_note)
removed_hashes   (video_id, sha256, takedown_id, removed_at)
audit_log        (id, actor_user_id, action, target_kind, target_id, detail_json, at)
encryption_keys  (id, wrapped_key, created_at, retired_at)  -- 同 Client 的信封加密
```

- 權限沒有獨立的表：群組的權限存在 `groups.permissions`，站台可見度、內容預設、自動規則與限流數字在設定檔。
- 限流用記憶體的 token bucket（單一程序）；多個 Hub 程序時改用 Postgres 的計數表，M5 不做。

## 考慮過的選項

- **依已驗證的 email 自動合併**：Google 的 email 有 `email_verified`，但 GitHub、Linux.do 的不一定；只對部分供應商合併會讓規則難以說明，帳號接管的後果又很嚴重。
- **PAT 權限在建立時固定**：帳號被移出群組後，舊 PAT 仍有權限，管理員要另外撤銷。
- **版本鍵不含 sha256（同一部影片、同一種變體只有一個版本）**：無法保存不同人的不同校正結果，也無法比較。

## 影響

- 好處：
  - 帳號接管只能透過已登入且剛驗證過的 session，攻擊面清楚。
  - 去重與衍生關係都以內容 hash 為準，Client 與 Hub 不需要共用 ID。
- 代價：
  - 使用者用兩個登入方式各登入一次，會得到兩個帳號，要自己用「連結」合併登入方式（只能在其中一個帳號沒有任何分享時，先刪除那個帳號；刪除帳號不在 M5 範圍內）。
  - `model_verified: true` 的意義有限，UI 文案要避免讓人誤以為 Hub 驗證過模型。

## 修訂

- 2026-11-24（安全修補 A，依 /goal 採 advisor 建議，待 owner 追認）：第 3 點「相同的鍵只存一份」改為「相同的鍵只在新上傳者看得到、可見度相同時共用一個版本」；看不到（別人的 `private`、看不到的群組、`hidden`）或可見度不同時另建版本、只共用 blob，`withdrawn` 的版本以新分享的值恢復。細節見 HREP 待決問題 R8「去重與可見度」。
