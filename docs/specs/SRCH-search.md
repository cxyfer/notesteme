# SRCH：字幕全文搜尋

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 14、16、17 節；[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md) 第 7 點；[LIB 規格](LIB-library.md) LIB-R3；[MCP 規格](MCP-tools.md) MCP-R6；UI [媒體庫](../ui/pages.md#3-媒體庫)的「字幕全文搜尋」
- 驗收場景：[`features/SRCH-search.feature`](../../features/SRCH-search.feature)

## 目的

在所有影音的字幕中找出「哪一部影片、哪一句講過某段話」，結果直接帶時間戳與字幕句編號，可以跳到那一句。搜尋的輸入是不可信的文字，不能被當成 FTS5 的查詢語法；索引只反映現行版本，被取代的舊字幕、垃圾桶與永久刪除的影音都不會出現在結果中。

## User stories

| ID | Story |
|---|---|
| SRCH-01 | As the owner, I want to search inside every transcript, so that I can find the video where something was said. |

## 規則

| ID | 規則 |
|---|---|
| SRCH-R1 | 每部影音、每個語言只索引**一份**字幕，就是這個語言目前「處理得最完整」的現行版本：原文語言依序取 `edited` > `corrected` > `segmented` > `normalized` > `source`（與 CHAP-R2 相同，多了 `source` 作為最後的後備）；每個譯文語言依序取 `edited` > `translated`。被取代的舊版本與順位較低的變體都不在索引中。結果的 `variant`、`lang` 標示命中的是哪一份。 |
| SRCH-R2 | 索引在產出寫入的**同一個寫入交易**中更新：新的現行版本（或順位更高的新變體）寫入時，刪除這部影音、這個語言舊的索引列並寫入新的。交易成功之後的第一個搜尋就看得到新內容、看不到舊內容；交易失敗時兩邊都不變。 |
| SRCH-R3 | 去掉前後空白後，查詢長度以 Unicode code point 計算：3 個以上用 FTS5 trigram 索引（`cues_fts MATCH ?`）；1～2 個改用掃描（`instr` 比對 `search_cues.text_fold`），所以 2 個字的中文詞也找得到。兩條路徑的語意相同：不分大小寫（Go 的 `strings.ToLower`，與 LIB-R3 一致）的子字串比對，回傳的結果與排序相同。 |
| SRCH-R4 | 使用者輸入一律當作**一個字面上的片語**：把內部的 `"` 換成 `""`，整串包上一對 `"`，以參數綁定傳給 `MATCH`；不加欄位前綴、不加 `*`。所以 `OR`、`AND`、`NOT`、`NEAR(…)`、`text:`、`*`、`^`、`-`、不成對的 `"`、`(`、`)` 都照字面比對，不是查詢語法。這些輸入一律回 200（找不到就是 0 筆），不會回 422 或 500。 |
| SRCH-R5 | 查詢的上限：去掉前後空白後是空字串時回 422 `invalid_request`，訊息「請輸入要搜尋的文字。」；超過 200 個 code point 時回 422 `invalid_request`，訊息「搜尋文字最多 200 個字。」；含控制字元（`\p{Cc}`，Tab 除外）時回 422 `invalid_request`，訊息「搜尋文字含有無法使用的字元。」。`limit` 為 1～100，預設 20，超出範圍回 422，`errors[].path` 為 `limit`。分頁用游標，格式與失效規則同 LIB 的 `GET /api/items`（HMAC、server 重啟後失效、錯誤訊息「游標不正確或已失效，請從第一頁重新讀取。」）。 |
| SRCH-R6 | 每筆結果是一個字幕句：`{item: {id, ref, title}, lang, variant, cue, start, end, text, highlights, before, after}`。`cue` 是該句在這份字幕中的編號（從 1 起，與 `GET /api/items/:id/cues` 的順序相同）；`start`、`end` 為 `HH:MM:SS.mmm`；`text` 是字幕句的純文字原文，**不插入任何 HTML 或標記**；`highlights` 是 `[{start, end}]`，為命中範圍在 `text` 中的位移，單位是 **UTF-16 code unit**（前端 `String.prototype.slice` 可以直接用），依序、不重疊，列出這一句中所有命中的位置；`before`、`after` 是前一句與後一句的純文字（沒有時省略）。回應是 `{results, total, next_cursor}`，`total` 是符合條件的字幕句總數，最後一頁省略 `next_cursor`。 |
| SRCH-R7 | 排序：影音依 `created_at` 由新到舊、相同時依 `id`；同一部影音內原文語言在前、譯文依語言代碼排序；同一份字幕內依 `cue` 由小到大。FTS5 的 `rank` 不使用（掃描路徑沒有 rank，兩條路徑的排序必須相同）。 |
| SRCH-R8 | 在垃圾桶中的影音不出現在結果與 `total` 中；還原後不需要重建索引就再次出現（索引列保留，查詢時排除 `deleted_at` 不為空的影音）。永久刪除（DEL-R4、DEL-R5）在同一個交易中刪除這部影音的所有索引列。 |
| SRCH-R9 | 簡繁與全形半形不互通：不做 OpenCC 轉換，也不做 NFKC，「假装」找不到「假裝」（與 LIB 待決問題中的標題搜尋一致）。可以用 `lang` 參數（BCP 47，完全相等）只搜尋某個語言。 |
| SRCH-R10 | 介面與 scope：`GET /api/search?q=&mode=fulltext[&lang=][&limit=][&cursor=]` 需要 `read`；`mode` 可以省略（預設 `fulltext`），其他值回 422 `unsupported_mode`，訊息「不支援這種搜尋方式：<mode>。」。MCP `search` 的 `mode: fulltext` 需要 `read`，參數為 `query`、`lang`、`limit`、`cursor`，結果與 API 相同；MCP 省略 `mode` 時仍是 `title`（MCP-R6，不改變 M2 的行為）。CLI `notesteme search <query> [--lang <l>] [--limit <n>] [--json]` 呼叫同一個 API，非 JSON 輸出每行為 `<ref>  <start>  <text>`。 |

## 介面

- API：`GET /api/search`（`read`；SRCH-R5、R6、R10）。
- MCP：`search` 加上 `mode: fulltext`（`read`）。
- CLI：`notesteme search <query> [--lang <l>] [--limit <n>] [--json]`；結束碼依 CLI-R6（422 為 2）。
- UI：媒體庫的搜尋欄（pages.md 第 3 節「字幕全文搜尋」）：結果以字幕句為單位，顯示時間碼、前後文與 `highlights` 標示的命中範圍；點擊打開影音詳情並定位到那一句（LIB-R6）。
- 資料表方向（ADR-0021 第 7 點）：
  - `search_cues (id INTEGER PRIMARY KEY, item_id, artifact_id, lang, variant, cue, start_ms, end_ms, text, text_fold)`：一列一個字幕句，只含 SRCH-R1 選出的那一份；`(item_id, lang)` 有索引。
  - `cues_fts`：`CREATE VIRTUAL TABLE cues_fts USING fts5(text_fold, content='search_cues', content_rowid='id', tokenize='trigram case_sensitive 1')`（外部內容表；`text_fold` 已由 Go 轉成小寫，見 S3）。寫入、刪除 `search_cues` 時同一個交易中以 trigger 同步 `cues_fts`。
  - migration 建立資料表後，以現有的現行產出建立一次索引（同一支 migration 內完成；資料量是個人規模）。
- 資源類別：不進佇列，同步請求。
- 對 `pipeline.Stages`／`outputs` 的影響：無；只在 artifact store 寫入 transcript 產出的交易中多一個 hook。

## 與其他規格的關係

- LIB-R3 的標題搜尋（`GET /api/items?q=`）不變；全文搜尋是另一條路由。
- MCP-R6：M2 的 `search` 對 `mode: title` 以外的值回 `unsupported_mode`；M4 起 `fulltext` 是合法值，其他值（例如 `semantic`）仍回 `unsupported_mode`，既有場景 `Search by title and refuse modes that do not exist yet` 不受影響。MCP 規格本身不在這次修改，實作切片完成時在 MCP-R6 的待決問題補一行指向本規格。
- DEL：移到垃圾桶、還原、永久刪除的行為不變；SRCH-R8 只規定索引如何配合。
- OUT-R13（匯入 `edited`）、COR、TRN、重跑（LIB-R11）：寫入新版本時觸發 SRCH-R2。
- BND：匯入 bundle 寫入的字幕產出一樣觸發 SRCH-R2。

## 不在範圍內

- 語意搜尋（embedding），`mode: semantic` 仍回 `unsupported_mode`（plan 第 3 節）。
- 筆記、章節、畫面說明的全文搜尋（只搜字幕）。
- 簡繁互通、拼音、模糊比對、同義詞（SRCH-R9）。
- 依相關度排序（SRCH-R7）。
- 儲存檢視（`GET/POST /api/views`，屬於 LIB 的另一個切片）。

## 待決問題

- S1：沒有校正字幕時索引哪一份（SRCH-R1）。plan 第 14 節只寫「只索引現行版本的校正字幕與譯文字幕」，照字面做的話，`correct` 關閉或沒有設定 provider 的影音（M1 的預設）完全搜不到。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：比照 CHAP-R2，每個語言索引處理得最完整的那一份，再加上 `source` 作為最後的後備；plan 第 14 節的這一句以本規格為準，實作切片同步修訂 plan。
- S2：`highlights` 的位移單位（SRCH-R6）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：UTF-16 code unit，因為唯一的消費者是瀏覽器（`slice`）與 MCP 的 JSON 用戶端；Go 端計算時以 `utf16.RuneLen` 累加。命中範圍以 `strings.ToLower` 後的文字計算，再對回原文的位置；`ToLower` 會改變長度的字元（例如 `İ`）以原文的完整字元為單位標示。
- S3：不分大小寫的方式（SRCH-R3）。SQLite 的 `trigram` 以 `case_sensitive 0` 摺疊大小寫，但它的摺疊規則和 Go 的 `strings.ToLower` 不一定相同，掃描路徑的結果就可能不同。決定（2026-11-23，依 /goal 採保守預設，待 owner 追認）：索引的是 Go 已經轉成小寫的 `text_fold`，查詢也先轉小寫，FTS5 以 `tokenize='trigram case_sensitive 1'` 建立，兩條路徑都只比對同一份 `text_fold`。
- S4：排序不用相關度（SRCH-R7）。trigram 的 bm25 對中文的意義不大，而且掃描路徑沒有 rank；個人規模的資料量下，「最近新增的影音在前」比較好預測。之後要相關度排序時另立規則。
- S5：結果以字幕句為單位，一部影音命中很多句時會佔滿第一頁。M4 不做「每部影音最多 N 句」的折疊，UI 可以在前端依影音分組顯示；之後視使用情況加 `group_by=item`。
- S6：簡繁不互通（SRCH-R9）。對 zh-Hant 與 zh-Hans 混用的使用者不方便，但同時索引兩種字形會讓 `highlights` 與原文對不上。之後評估時與 LIB 的標題搜尋一起決定。
- S7：查詢上限 200 個 code point（SRCH-R5）與標題搜尋相同；trigram 的查詢成本隨片語長度線性增加，200 字不會造成問題。
