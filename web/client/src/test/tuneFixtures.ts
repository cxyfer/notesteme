// UI-3a（Prompt 庫、處理方案、字幕樣式）的測試資料。
import type { components } from "../api/schema";

type S = components["schemas"];

export function line(over: Partial<S["ASSLine"]> = {}): S["ASSLine"] {
  return {
    font: "Noto Sans",
    size: 52,
    color: "#FFFFFF",
    outline_color: "#000000",
    back_color: "#000000",
    outline: 2.5,
    shadow: 0,
    bold: false,
    ...over,
  };
}

export function style(id: string, over: Partial<S["StyleBody"]> = {}): S["StyleBody"] {
  return {
    id,
    name: id,
    builtin: false,
    primary: line({ size: 40 }),
    translation: line(),
    layout: {
      show: "both",
      order: "translation-first",
      translation_at: "stacked",
      alignment: 2,
      margin_v: 48,
      border_style: 1,
    },
    ...over,
  };
}

export function profile(name: string, over: Partial<S["Profile"]> = {}): S["Profile"] {
  return {
    name,
    version: 1,
    config: {},
    builtin: false,
    default: false,
    created_at: "2026-11-20T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    ...over,
  };
}

export function promptSummary(
  id: string,
  over: Partial<S["PromptSummary"]> = {},
): S["PromptSummary"] {
  return {
    id,
    kind: "correct",
    builtin: false,
    default_version: 1,
    latest_version: 1,
    created_at: "2026-11-20T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    ...over,
  };
}

export function promptVersion(version: number, body: string): S["PromptVersionBody"] {
  return {
    version,
    body,
    sha256: `sha-${version}`,
    variables: [],
    created_at: "2026-11-21T10:00:00Z",
  };
}
