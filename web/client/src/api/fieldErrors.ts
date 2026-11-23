// 422 的逐欄位錯誤（Error.errors[].path／message）→ 以 path 為鍵的表，表單用它在欄位旁顯示訊息。
// path 是後端的點號路徑，例如 stages.correct.provider、primary.size。
import { isApiError } from "./errors";

export type FieldErrors = Record<string, string>;

export function fieldErrorsOf(err: unknown): FieldErrors {
  if (!isApiError(err) || err.status !== 422) return {};
  const out: FieldErrors = {};
  for (const e of err.body.errors ?? []) {
    if (e.path && !(e.path in out)) out[e.path] = e.message;
  }
  return out;
}
