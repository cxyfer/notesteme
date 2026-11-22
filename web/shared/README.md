# web/shared

Client UI（`web/client`）與之後的 Hub UI（`web/hub`，M5）共用的元件，例如時間帶、字幕句列表（ADR-0008 決策 9）。

UI-0 只建立目錄。第一個共用元件（時間帶，UI-1）完成並穩定後再搬進來；在那之前元件放在 `web/client/src/components`，
避免為了「之後可能共用」提早抽象。搬進來時：

- 不依賴 `web/client` 的 API client、router 或 i18n 實例：文字由呼叫端用 props 傳入。
- 設計 token 用 CSS 變數（`web/client/src/styles/tokens.css` 的名稱），由使用端提供。
