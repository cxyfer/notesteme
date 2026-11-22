# web/shared

Client UI（`web/client`）與之後的 Hub UI（`web/hub`，M5）共用的元件，例如時間帶、字幕句列表（ADR-0008 決策 9）。

## 規則

- 只依賴 React：不依賴 `web/client` 的 API client、router 或 i18n 實例，**所有文字由呼叫端用 props 傳入**。
- 設計 token 用 CSS 變數（`web/client/src/styles/tokens.css` 的名稱），由使用端提供。元件的 CSS 放在元件旁邊，由使用端匯入（Client 在 `src/main.tsx`）。
- 這裡沒有 `node_modules`：Client 以 `@shared` 匯入（`vite.config.ts` 的 alias 與 `resolve.dedupe`、`tsconfig.json` 的 `paths`）。
  lint 與 prettier 由 `web/client` 的 `npm run lint`、`npm run format:check` 一起檢查；測試放在 `web/client/src/shared/`。
- 擴充元件時加新的選用 props，不改變既有 props 的意義。

## 元件

### StatusBand（`src/StatusBand.tsx`、`src/status-band.css`）

時間帶的步驟帶（design-language.md §6.1）。UI-1 只有精簡版（`variant="compact"`）：24px 底軌（還不知道說話密度時畫一條點狀基線）加上每個步驟一條 3px 帶子，用在媒體庫的列。

| prop        | 型別                     | 說明                                                                                                                                                                                         |
| ----------- | ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `stages`    | `StatusBandStage[]`      | `{ key, status, label, segments? }`；`status` 是 `pending`、`running`、`waiting`、`awaiting_agent`、`done`、`skipped`、`failed`、`blocked`。`skipped` 不畫帶子。`label` 是那條帶子的 tooltip |
| `label`     | `string`                 | 整條帶子的文字摘要，放在 `role="img"` 的 `aria-label`（例如「字幕：完成，校正：失敗，翻譯：等待中」）                                                                                        |
| `durationS` | `number \| null`         | 影音長度（秒）；畫 `segments` 時需要，沒有時整條用 `stage.status`                                                                                                                            |
| `segments`  | `{ from, to, status }[]` | 依時間範圍的細分（例如 LLM 的 chunk，秒）；超出範圍的部分裁掉                                                                                                                                |
| `track`     | `ReactNode`              | 底軌上的內容（UI-2 的說話密度、章節）                                                                                                                                                        |
| `variant`   | `"compact"`              | UI-2 加入 `"full"`（72px 底軌、刻度、播放頭、`role="slider"`）                                                                                                                               |

每種狀態的紋理不同（實心、虛線、長虛線、點線、斜線紋），不只靠顏色；`prefers-reduced-motion` 時「顯影」改成靜態斜線紋。
