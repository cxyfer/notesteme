// 影音詳情的資料讀取。key 一律用影音 ID（detail.id），SSE 的失效表才對得上（路由參數可能是 yt:… 這種代號）。
import { keepPreviousData, useQuery } from "@tanstack/react-query";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import type { ApiStage } from "../library/stageView";
import { toCues, type Cue } from "./cues";
import type { CueMode } from "./search";

export type ApiRun = components["schemas"]["Run"];
export type ApiOutput = components["schemas"]["OutputFile"];

export interface CueSet {
  variant: string;
  lang: string;
  cues: Cue[];
}

/** 這個變體還沒有字幕（404 not_found）時回傳 null，不當成錯誤 */
async function fetchCues(
  id: string,
  q: { variant?: string; lang?: string },
  signal?: AbortSignal,
): Promise<CueSet | null> {
  try {
    const r = await api.get<"/api/items/{id}/cues">(apiPath("/api/items/{id}/cues", { id }), {
      query: q,
      signal,
    });
    return { variant: r.variant, lang: r.lang, cues: toCues(r.cues) };
  } catch (e) {
    if (isApiError(e) && e.status === 404 && e.code === "not_found") return null;
    throw e;
  }
}

/** 某個變體的字幕；id 是 undefined 時不讀取 */
export function useCues(id: string | undefined, variant: string | undefined, lang?: string) {
  const q = { ...(variant ? { variant } : {}), ...(lang ? { lang } : {}) };
  return useQuery({
    queryKey: queryKeys.items.cues(id ?? "", q),
    queryFn: ({ signal }) => fetchCues(id!, q, signal),
    enabled: !!id && !!variant,
    // 換模式時保留上一份字幕直到新的到達，列表不卸載，捲動位置才能接回來
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  });
}

/** 差異的比較基準：校正之前實際存在的最近變體（segmented → normalized → source） */
export const DIFF_BASES = ["segmented", "normalized", "source"] as const;

export function useDiffBase(id: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.items.cues(id ?? "", { variant: "diff-base" }),
    queryFn: async ({ signal }) => {
      for (const variant of DIFF_BASES) {
        const r = await fetchCues(id!, { variant }, signal);
        if (r && r.cues.length > 0) return r;
      }
      return null;
    },
    enabled: !!id && enabled,
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  });
}

export function useRuns(id: string | undefined) {
  return useQuery({
    queryKey: queryKeys.items.runs(id ?? ""),
    queryFn: ({ signal }) =>
      api.get<"/api/items/{id}/runs">(apiPath("/api/items/{id}/runs", { id: id! }), { signal }),
    enabled: !!id,
    staleTime: 30_000,
  });
}

export function useOutputs(id: string | undefined) {
  return useQuery({
    queryKey: queryKeys.items.outputs(id ?? ""),
    queryFn: ({ signal }) =>
      api.get<"/api/items/{id}/outputs">(apiPath("/api/items/{id}/outputs", { id: id! }), {
        signal,
      }),
    enabled: !!id,
    staleTime: 30_000,
  });
}

/** 每個顯示模式要讀的變體 */
export function modeVariant(mode: CueMode): string {
  switch (mode) {
    case "diff":
      return "corrected";
    case "parallel":
      return "translated";
    default:
      return mode;
  }
}

/** 依步驟狀態判斷哪些模式有內容可看（沒有的模式不顯示） */
export function availableModes(stages: readonly ApiStage[], hasEdited: boolean): CueMode[] {
  const st = new Map(stages.map((s) => [s.stage, s.status]));
  const done = (s: string) => st.get(s) === "done";
  const started = (s: string) => ["done", "running", "failed", "blocked"].includes(st.get(s) ?? "");
  const out: CueMode[] = [];
  if (done("transcript")) out.push("source");
  if (done("normalize")) out.push("normalized");
  // 校正處理中也能看已完成的段落（pages.md §4「校正還沒完成時…」）
  if (started("correct") && st.get("correct") !== "skipped") out.push("corrected", "diff");
  if (done("translate")) out.push("translated", "parallel");
  if (hasEdited) out.push("edited");
  return out;
}

/**
 * 預設模式：校正（只在校正完成時）> 正規化 > 原始。校正處理中或失敗時 corrected 可能還不存在，
 * 預設選它會讓字幕分頁是空的。
 */
export function defaultMode(
  modes: readonly CueMode[],
  stages: readonly ApiStage[] = [],
): CueMode | undefined {
  const correctDone = stages.some((s) => s.stage === "correct" && s.status === "done");
  for (const m of ["corrected", "normalized", "source", "edited", "translated"] as const) {
    if (m === "corrected" && !correctDone) continue;
    if (modes.includes(m)) return m;
  }
  return modes[0];
}

/** 最近一次執行的生效設定（GET /runs 由新到舊） */
export function latestConfig(
  runs: readonly ApiRun[] | undefined,
  stage?: string,
): Record<string, unknown> | undefined {
  const r = (runs ?? []).find((x) => !stage || x.stage === stage);
  const c = r?.config;
  return c && typeof c === "object" && !Array.isArray(c)
    ? (c as Record<string, unknown>)
    : undefined;
}

/** 翻譯的目標語言（設定快照的 language.targets） */
export function targetLangs(runs: readonly ApiRun[] | undefined): string[] {
  const lang = latestConfig(runs)?.language as { targets?: unknown } | undefined;
  return Array.isArray(lang?.targets)
    ? lang.targets.filter((x): x is string => typeof x === "string")
    : [];
}

/** 某個步驟在設定快照裡的 executor／provider／model／prompt（來源紀錄摘要） */
export interface StageConfig {
  executor?: string;
  provider?: string;
  model?: string;
  prompt?: string;
}

export function stageConfig(run: ApiRun | undefined): StageConfig {
  const cfg = run?.config as { stages?: Record<string, unknown> } | undefined;
  const raw = cfg?.stages?.[run!.stage];
  if (!raw || typeof raw !== "object") return {};
  const r = raw as Record<string, unknown>;
  const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);
  return {
    executor: str(r.executor),
    provider: str(r.provider),
    model: str(r.model),
    prompt: str(r.prompt),
  };
}
