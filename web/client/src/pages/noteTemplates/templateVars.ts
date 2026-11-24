// 筆記模板（Go text/template 的受限子集，NOTE-R11、N17）的變數掃描，給 PromptCodeEditor 高亮與標示錯誤。
//
// 只找動作 {{ … }}（含 {{- 與 -}}）中的欄位參照 .Name 或 .Item.URL：在清單中的高亮，不在清單中的標成
// 「不認識的變數」（與後端的訊息相同）。if／else／end 等關鍵字、字串常數與其他語法不在這裡判斷，交給儲存或預覽時的
// 後端檢查：寧可少標，也不要把合法的寫法標成錯誤。沒有對應「}}」的「{{」標成無法解析，之後不再找。
import type { VarIssue, VarToken } from "../prompts/promptModel";

/** NOTE-R11：模板可以用的變數（與 internal/notes 一致；後端改了要同步） */
export const TEMPLATE_VARS = [
  ".Frontmatter",
  ".Title",
  ".ReadButton",
  ".InfoTable",
  ".Sections",
  ".SelfCheck",
  ".Lang",
  ".Item.URL",
  ".Item.Channel",
  ".Item.Published",
  ".Item.Duration",
] as const;

// 欄位參照：前面不能是名稱字元（避免把 a.b 的 .b 當成欄位），之後是一段或多段 .名稱
const FIELD_RE = /(?<![A-Za-z0-9_)\]])\.[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*/g;
// 字串常數（"…"、`…`）與字元常數（'…'）：換成等長的空白，不在裡面找欄位
const LITERAL_RE = /"(?:[^"\\]|\\.)*"|`[^`]*`|'(?:[^'\\]|\\.)*'/g;

export function scanTemplateVariables(
  body: string,
  known: readonly string[] = TEMPLATE_VARS,
): { tokens: VarToken[]; issues: VarIssue[] } {
  const tokens: VarToken[] = [];
  const issues: VarIssue[] = [];
  let pos = 0;
  for (;;) {
    const i = body.indexOf("{{", pos);
    if (i < 0) break;
    const end = body.indexOf("}}", i + 2);
    if (end < 0) {
      issues.push({ from: i, to: body.length, kind: "unclosed", name: "" });
      break;
    }
    const inner = body.slice(i + 2, end).replace(LITERAL_RE, (m) => " ".repeat(m.length));
    // 註解 {{/* … */}} 不是動作
    if (!/^-?\s*\/\*/.test(inner)) {
      for (const m of inner.matchAll(FIELD_RE)) {
        const from = i + 2 + m.index;
        const to = from + m[0].length;
        const ok = known.includes(m[0]);
        tokens.push({ from, to, name: m[0], known: ok });
        if (!ok) issues.push({ from, to, kind: "undefined", name: m[0] });
      }
    }
    pos = end + 2;
  }
  return { tokens, issues };
}
