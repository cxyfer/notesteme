// LLM chunk 的時間範圍（LIB-R29，GET /api/items/{id}/chunks）：
//   - 時間帶的步驟帶依 chunk 的時間範圍畫「處理中」「失敗」（design-language.md §6.1）；底軌的處理範圍由 StatusBand 依
//     segments 畫出。帶子的標籤照 §6.1 狀態表的文字（「校正 9/16」「失敗：第 7 段」），完整的段落與時間另外寫成文字版本。
//   - 「重新校正這段」：拖曳選取的時間範圍由伺服器換成 chunk 編號（from、to 的重疊規則只寫在後端）。
// chunk 任務完成不發 SSE（LIB-R24 同樣的限制）：有 LLM 步驟 running 時每 15 秒重新讀取，stage.status 與重新連上時失效。
import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";

import type { BandSegment, BandStatus } from "@shared";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { formatTimecode } from "../../i18n";
import type { ApiStage } from "../library/stageView";
import { parseCueTime } from "./cues";

export type ApiChunk = components["schemas"]["ChunkSpanBody"];

/** 有 chunk 的 LLM 步驟 */
const CHUNK_STAGES = new Set(["segment", "correct", "translate"]);
/** 有 LLM 步驟 running 時重新讀取的間隔 */
export const CHUNK_POLL_MS = 15_000;

export function useChunks(id: string, stages: readonly ApiStage[]) {
  const llm = stages.filter((s) => CHUNK_STAGES.has(s.stage));
  // 還沒開始（pending）或略過的步驟沒有切分；只有在至少一個 LLM 步驟開始過時才讀
  const enabled = llm.some((s) => s.status !== "pending" && s.status !== "skipped");
  const running = llm.some((s) => s.status === "running");
  return useQuery({
    queryKey: queryKeys.items.chunks(id),
    queryFn: async ({ signal }) =>
      (
        await api.get<"/api/items/{id}/chunks">(apiPath("/api/items/{id}/chunks", { id }), {
          signal,
        })
      ).chunks ?? [],
    enabled,
    staleTime: 10_000,
    refetchInterval: running ? CHUNK_POLL_MS : false,
  });
}

/** 時間範圍內的校正 chunk 編號（伺服器依 [from, to) 的重疊換算） */
export async function correctChunksIn(id: string, from: number, to: number): Promise<number[]> {
  const r = await api.get<"/api/items/{id}/chunks">(apiPath("/api/items/{id}/chunks", { id }), {
    query: { stage: "correct", from: round3(from), to: round3(to) },
  });
  return (r.chunks ?? []).map((c) => c.chunk).sort((a, b) => a - b);
}

function round3(s: number) {
  return Math.round(s * 1000) / 1000;
}

const SPAN_BAND: Record<ApiChunk["status"], BandStatus> = {
  done: "done",
  // 退回原文（LLM-R5）的 chunk 也算處理完：完成的帶子刻意保持安靜（README 10.30 第 3 點）
  fallback: "done",
  running: "running",
  failed: "failed",
  pending: "pending",
};

interface TimedChunk {
  chunk: number;
  status: ApiChunk["status"];
  from: number;
  to: number;
}

/**
 * 一個步驟目前這次執行的 chunk（current，有時間）；translate 有多個目標語言時回傳空的
 * （同一段時間有多個目標，一條帶子畫不下，維持整條一個狀態）。
 */
export function stageChunks(chunks: readonly ApiChunk[] | undefined, stage: string): TimedChunk[] {
  const own = (chunks ?? []).filter((c) => c.stage === stage && c.current);
  if (new Set(own.map((c) => c.lang ?? "")).size > 1) return [];
  const out: TimedChunk[] = [];
  for (const c of own) {
    const from = c.start ? parseCueTime(c.start) : NaN;
    const to = c.end ? parseCueTime(c.end) : NaN;
    if (Number.isFinite(from) && Number.isFinite(to) && to > from)
      out.push({ chunk: c.chunk, status: c.status, from, to });
  }
  // 有任何一個 chunk 沒有時間就不畫（畫一部分會讓沒畫到的時間看起來像沒有內容）
  return out.length === own.length ? out.sort((a, b) => a.chunk - b.chunk) : [];
}

export function bandSegments(chunks: readonly TimedChunk[]): BandSegment[] {
  return chunks.map((c) => ({ from: c.from, to: c.to, status: SPAN_BAND[c.status] }));
}

/** 「2, 5–7」：連續的編號合成一段（重新處理對話框的 chunk 欄位也接受這個格式） */
export function chunkList(nums: readonly number[], sep = ", "): string {
  const sorted = [...new Set(nums)].sort((a, b) => a - b);
  const parts: string[] = [];
  for (let i = 0; i < sorted.length;) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j]! + 1) j++;
    parts.push(i === j ? String(sorted[i]) : `${sorted[i]}–${sorted[j]}`);
    i = j + 1;
  }
  return parts.join(sep);
}

/**
 * 帶子右側的標籤（§6.1 的狀態表）：有失敗的 chunk 時是「校正：失敗：第 7 段」，處理中是「校正 9/16」；
 * 其他情況回傳 undefined（沿用整條的狀態文字）。
 */
export function chunkLabel(
  t: TFunction,
  stageName: string,
  stageStatus: string | undefined,
  chunks: readonly TimedChunk[],
): string | undefined {
  if (!chunks.length) return undefined;
  const failed = chunks.filter((c) => c.status === "failed").map((c) => c.chunk);
  if (failed.length)
    return t("item.band.chunkFailed", { stage: stageName, chunks: chunkList(failed) });
  if (stageStatus !== "running") return undefined;
  const done = chunks.filter((c) => c.status === "done" || c.status === "fallback").length;
  return t("item.band.chunkProgress", { stage: stageName, done, total: chunks.length });
}

/** 文字版本：「校正：第 1–8 段完成（00:00–07:47），第 9 段處理中（07:47–08:45），第 10–16 段未開始（…）。」 */
export function chunkSummary(
  t: TFunction,
  stageName: string,
  chunks: readonly TimedChunk[],
): string {
  if (!chunks.length) return "";
  const runs: TimedChunk[][] = [];
  for (const c of chunks) {
    const last = runs[runs.length - 1];
    if (last && last[last.length - 1]!.status === c.status) last.push(c);
    else runs.push([c]);
  }
  const parts = runs.map((r) =>
    t("item.band.chunkRun", {
      chunks: chunkList(r.map((c) => c.chunk)),
      status: t(`item.band.chunkStatus.${r[0]!.status}`),
      from: formatTimecode(r[0]!.from),
      to: formatTimecode(r[r.length - 1]!.to),
    }),
  );
  return t("item.band.chunkSummary", { stage: stageName, runs: parts.join(t("item.band.sep")) });
}
