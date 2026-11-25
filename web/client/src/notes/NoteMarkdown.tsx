// 轉接：NoteMarkdown 已搬到 web/shared/src/notes（M5-S9，Hub UI 共用）；以檔案路徑匯入，留在 lazy chunk。
// Client 在這裡另外載入閱讀區的字型（readingFonts.ts）。不放進 web/shared：Hub 有自己的風格與字型。
import {
  NoteMarkdown as SharedNoteMarkdown,
  type NoteMarkdownProps,
} from "@shared/notes/NoteMarkdown";

import { useReadingFonts } from "./readingFonts";

export { timeLinkSeconds, type NoteMarkdownProps } from "@shared/notes/NoteMarkdown";

export function NoteMarkdown(props: NoteMarkdownProps) {
  useReadingFonts(props.lang);
  return <SharedNoteMarkdown {...props} />;
}
