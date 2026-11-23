# web/shared

Client UI（`web/client`）與之後的 Hub UI（`web/hub`，M5）共用的元件，例如時間帶、字幕句列表（ADR-0008 決策 9）。

## 規則

- 只依賴 React：不依賴 `web/client` 的 API client、router 或 i18n 實例，**所有文字由呼叫端用 props 傳入**。
- 設計 token 用 CSS 變數（`web/client/src/styles/tokens.css` 的名稱），由使用端提供。元件的 CSS 放在元件旁邊，由使用端匯入（Client 在 `src/main.tsx`）。
- 這裡沒有 `node_modules`：Client 與 Hub（web/hub）以 `@shared` 匯入（`vite.config.ts` 的 alias 與 `resolve.dedupe`、`tsconfig.json` 的 `paths`）。
  lint 與 prettier 由 `web/client` 的 `npm run lint`、`npm run format:check` 一起檢查；測試放在 `web/client/src/shared/`。
- 擴充元件時加新的選用 props，不改變既有 props 的意義。

## 元件

### StatusBand（`src/StatusBand.tsx`、`src/status-band.css`）

時間帶的步驟帶（design-language.md §6.1）。

- 精簡版（`variant="compact"`，UI-1）：24px 底軌（還不知道說話密度時畫一條點狀基線）加上每個步驟一條 3px 帶子，用在媒體庫的列，`role="img"`。
- 完整版（`variant="full"`，UI-2）：72px 底軌、時間刻度、6px 步驟帶與右側逐列對齊的文字標籤、播放頭、處理範圍（running／failed 的 segments 淡淡標在底軌）。整條是 `role="slider"`：`aria-valuetext` 是時間碼，←／→（與 ↑／↓）移動 `stepS` 秒、PageUp／PageDown 移動 6 倍、Home／End 到頭尾；點擊跳到該時間；hover 顯示時間碼與 `describeTime` 的說明。沒有長度時 `aria-disabled`。

| prop        | 型別                     | 說明                                                                                                                                                                                         |
| ----------- | ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `stages`    | `StatusBandStage[]`      | `{ key, status, label, segments? }`；`status` 是 `pending`、`running`、`waiting`、`awaiting_agent`、`done`、`skipped`、`failed`、`blocked`。`skipped` 不畫帶子。`label` 是那條帶子的 tooltip |
| `label`     | `string`                 | 整條帶子的文字摘要，放在 `role="img"` 的 `aria-label`（例如「字幕：完成，校正：失敗，翻譯：等待中」）                                                                                        |
| `durationS` | `number \| null`         | 影音長度（秒）；畫 `segments` 時需要，沒有時整條用 `stage.status`                                                                                                                            |
| `segments`  | `{ from, to, status }[]` | 依時間範圍的細分（例如 LLM 的 chunk，秒）；超出範圍的部分裁掉                                                                                                                                |
| `track`     | `ReactNode`              | 底軌上的內容（UI-2 的說話密度、章節）                                                                                                                                                        |
| `variant`   | `"compact" \| "full"`    | 預設 `compact`                                                                                                                                                                               |

完整版另外的 props（compact 忽略）：

| prop           | 型別                    | 說明                                                                |
| -------------- | ----------------------- | ------------------------------------------------------------------- |
| `position`     | `number`                | 播放頭（秒）                                                        |
| `onSeek`       | `(seconds) => void`     | 點擊或按鍵移動播放頭；值已限制在 `[0, durationS]`                   |
| `formatTime`   | `(seconds) => string`   | 時間碼格式（刻度、`aria-valuetext`、hover）；web/shared 不依賴 i18n |
| `valueText`    | `(formatted) => string` | `aria-valuetext` 的完整文字，例如「播放位置 01:05」                 |
| `describeTime` | `(seconds) => string`   | hover 的說明（例如那個時間點的字幕句）；空字串不顯示                |
| `describeLang` | `string`                | 說明的內容語言，設在元素的 `lang`                                   |
| `stepS`        | `number`                | 方向鍵的秒數，預設 5                                                |

還沒做（API 沒有資料）：章節線與 `Shift + ←／→` 跳章節、拖曳選取範圍、說話密度、畫面刻度。

每種狀態的紋理不同（實心、虛線、長虛線、點線、斜線紋），不只靠顏色；`prefers-reduced-motion` 時「顯影」改成靜態斜線紋。

### 逐字差異（`src/diff.ts`、`src/DiffText.tsx`、`src/diff.css`）

`diffChars(before, after)` 以 code point 為單位的 LCS，回傳 `{ type: "eq" | "del" | "ins", text }[]`；太長（n·m > 4,000,000）時退化成整段刪除＋新增。`DiffText` 用 `<del>`／`<ins>` 顯示（刪除線＋失敗色、底線＋高亮），每段前面有 visually-hidden 的 `labels.del`／`labels.ins`，內容一律是純文字。UI-3a 的試跑結果使用；UI-2 分支的 `pages/item/diff.ts` 形狀相同，合併時改成從這裡匯入。
