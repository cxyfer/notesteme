# ADR-0008：API-first，React SPA 嵌入 Go binary

- 狀態：已採納
- 日期：2026-11-11

## 背景

- 所有功能都要能從 UI 操作，CLI 和 MCP 的能力也要相同。
- UI 有幾個互動比較複雜的元件：
  - 時間帶：可以拖曳、和播放器同步。
  - 字幕對照。
  - Prompt 試跑與 diff。
  - ASS 樣式預覽。
  - 大量項目的篩選表格。
- UI 要有清楚的設計語言，不要看起來像現成的元件套件（見 [UI 設計語言](../ui/design-language.md)）。

## 決策

1. **API-first**：
   - UI、CLI、MCP 都呼叫同一層 service。
   - HTTP API 用 Huma v2 定義，自動產生 OpenAPI 3.1。
   - 前端的 TypeScript client 用 `openapi-typescript` 從 OpenAPI 產生。
2. **前端技術**：
   - React + Vite + TypeScript。
   - TanStack Router：篩選條件存在 URL 參數裡，可以加書籤或分享。
   - TanStack Query：資料快取。
   - TanStack Table：大型列表用虛擬捲動。
3. **元件**：
   - 用 Radix primitives 處理行為與無障礙。
   - shadcn/ui 只取元件骨架，**全部改用自家的 design token**。不用預設的 zinc 色盤，也不讓所有東西用同一個圓角。
4. **編輯器與 diff**：CodeMirror 6。
5. **即時更新**：用 SSE 推送任務進度與事件，收到後讓 TanStack Query 的快取失效並重新取得資料。
6. **i18n**：
   - i18next，支援繁中、简中、English。
   - `<html lang>` 依 UI 語言設定，並依語言套用不同的字型堆疊（中文字的字形依地區不同）。
7. **打包**：`vite build` 的產物用 `embed.FS` 嵌入 Go binary，同源提供，不需要 CORS。
8. **開發**：Vite dev server 代理 `/api` 與 `/mcp` 到 Go 後端；後端用 `air` 熱重載。
9. Client UI 放在 `web/client`，Hub UI 放在 `web/hub`，共用元件（時間帶、字幕列表）放在 `web/shared`。

## 考慮過的選項

- **htmx + templ（伺服器端渲染）**：不需要前端建置，但時間帶、播放器同步、diff 這類互動要寫很多手工的 JS。
- **Svelte 5／SvelteKit**：更輕量，但 TanStack Table、Router 和 Radix 這一套生態系在 React 上最完整。
- **桌面 App（Wails、Tauri）**：主要部署在 Docker 和 VPS 上，用瀏覽器存取比較合適。

## 影響

- 好處：
  - UI 和 CLI、MCP 的功能一致。
  - 型別從後端一路產生到前端，不會不一致。
  - 前後端都有秒級的開發回饋。
- 代價：
  - 需要 Node 工具鏈來建置前端。Docker 多階段建置可以處理。
  - 自訂設計語言要多花一些工夫，不能直接用預設樣式。
