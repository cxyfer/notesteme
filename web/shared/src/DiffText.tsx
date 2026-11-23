// 逐字差異的顯示：刪除的字用 <del>、新增的字用 <ins>，一律是純文字（NFR-SEC-2）。
// <ins>/<del> 多數螢幕閱讀器不會唸出，所以每段前面加上 visually-hidden 的標籤（由呼叫端傳入文字）。
import type { DiffPart } from "./diff";

export interface DiffTextProps {
  parts: readonly DiffPart[];
  /** 螢幕閱讀器用的標籤，例如「刪除：」「新增：」 */
  labels: { del: string; ins: string };
  /** 內容的語言（字幕自己的 lang，docs/ui/README.md §7） */
  lang?: string;
}

export function DiffText({ parts, labels, lang }: DiffTextProps) {
  return (
    <span className="diff" lang={lang}>
      {parts.map((p, i) =>
        p.type === "eq" ? (
          <span key={i}>{p.text}</span>
        ) : p.type === "del" ? (
          <del key={i} className="diff__del">
            <span className="visually-hidden">{labels.del}</span>
            {p.text}
          </del>
        ) : (
          <ins key={i} className="diff__ins">
            <span className="visually-hidden">{labels.ins}</span>
            {p.text}
          </ins>
        ),
      )}
    </span>
  );
}
