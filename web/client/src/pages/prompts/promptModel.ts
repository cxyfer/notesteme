// Prompt 庫的模型：類型分組、可用變數與變數檢查（與後端 internal/prompts 的規則一致）。
import type { components } from "../../api/schema";

export type PromptSummary = components["schemas"]["PromptSummary"];
export type PromptVersion = components["schemas"]["PromptVersionBody"];

/** 列表的分組（pages.md §7：斷句、校正、翻譯、選圖、筆記；其他類型與不認得的類型歸入「其他」）。 */
export const PROMPT_GROUPS = [
  "segment",
  "correct",
  "translate",
  "frames",
  "note",
  "other",
] as const;
export type PromptGroup = (typeof PROMPT_GROUPS)[number];

/** kind → 分組。後端的 kind：summary、chapters、segment、correct、translate、frames.select、note（M3 另有 frames、note_map）。 */
export function groupOf(kind: string): PromptGroup {
  switch (kind) {
    case "segment":
    case "correct":
    case "translate":
      return kind;
    case "frames":
    case "frames.select":
      return "frames";
    case "note":
    case "note_map":
      return "note";
    default:
      return "other";
  }
}

/** 建立 prompt 時可選的類型（internal/prompts.Kinds）。 */
export const PROMPT_KINDS = [
  "segment",
  "correct",
  "translate",
  "frames.select",
  "note",
  "summary",
  "chapters",
] as const;

/**
 * 可用的變數（PRM-R3、internal/prompts.Variables）。後端目前所有類型共用同一份清單，
 * 沒有提供清單的 API，所以前端寫一份；後端改了要同步（見 docs/ui/README.md 10.9）。
 * 之後後端依類型區分時，在這裡改成依 kind 回傳。
 */
export const PROMPT_VARIABLES = [
  "title",
  "channel",
  "description",
  "chapters",
  "glossary",
  "source_lang",
  "target_lang",
] as const;

/** 變數名稱的格式（internal/prompts/vars.go 的 varNameRE）。 */
const VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_.]*$/;

export interface VarIssue {
  /** 字元位置（UTF-16 offset，CodeMirror 用） */
  from: number;
  to: number;
  kind: "undefined" | "invalid" | "unclosed";
  name: string;
}

export interface VarToken {
  from: number;
  to: number;
  name: string;
  known: boolean;
}

/**
 * 找出內文中的 {{ name }}：tokens 是格式正確的變數（含未定義的），issues 是要標示的錯誤。
 * 規則與後端相同：名稱前後可以有空白；未關閉的「{{」與格式不正確的名稱是「無法解析」，名稱不在清單中是「未定義」。
 * 後端遇到第一個無法解析的地方就停下；這裡未關閉的「{{」之後不再找，格式錯誤則繼續找下一個。
 */
export function scanVariables(
  body: string,
  known: readonly string[] = PROMPT_VARIABLES,
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
    const raw = body.slice(i + 2, end);
    const name = raw.trim();
    const to = end + 2;
    if (!VAR_NAME_RE.test(name)) {
      issues.push({ from: i, to, kind: "invalid", name: raw });
    } else {
      const ok = known.includes(name);
      tokens.push({ from: i, to, name, known: ok });
      if (!ok) issues.push({ from: i, to, kind: "undefined", name });
    }
    pos = to;
  }
  return { tokens, issues };
}

/** id 規則（CreatePromptInputBody）：小寫英文字母、數字、「.」與「-」。 */
export function validPromptId(id: string) {
  return /^[a-z0-9][a-z0-9.-]*$/.test(id) && id.length <= 64;
}
