# UI 設計方向 mockup

用來驗證 [設計語言](../design-language.md) 的靜態頁面，不是正式的前端程式碼。

| 頁面 | 內容 |
|---|---|
| `library.html` | 媒體庫：依頻道分組、精簡版時間帶、各種處理狀態 |
| `item.html` | 影音詳情：完整版時間帶（可 hover、點擊、用方向鍵移動播放頭）、播放器、章節、字幕句列表 |

## 開啟方式

```bash
python3 -m http.server 8765 --bind 127.0.0.1 --directory docs/ui/mockups
```

接著開啟 <http://127.0.0.1:8765/library.html>。

- 網址加上 `?theme=dark` 會切換成深色主題；右上角的按鈕也可以切換。
- 頁面需要網路，才能載入 Google Fonts、Phosphor Icons 和 YouTube 縮圖。

## 資料

`data.js` 由 `tools/gen_data.py` 從 Obsidian vault 的 `Video Notes/` 產生。內容是真實的影片標題、章節、選中畫面的時間、字幕密度，以及一部影片的 52 句字幕。

```bash
python3 -I docs/ui/mockups/tools/gen_data.py docs/ui/mockups/data.js
```

`data.js` 含有影片字幕的片段，預設不 commit。

各影片的處理狀態（轉錄中、等待 Agent、失敗等）是為了展示而模擬的，寫在 `gen_data.py` 的 `ITEMS` 裡。
