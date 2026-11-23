// 轉接：筆記的 Markdown 解析器已搬到 web/shared/src/notes（M5-S9，Hub UI 共用）。
// 以檔案路徑匯入、不經過 @shared 的 index：否則會被打包進初始載入的 chunk。
export {
  parseInline,
  parseMarkdown,
  safeHref,
  stripFrontmatter,
  type Block,
  type Inline,
} from "@shared/notes/markdown";
