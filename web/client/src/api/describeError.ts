import type { TFunction } from "i18next";

import { ApiError, CLIENT_CODES, isApiError } from "./errors";

/**
 * 錯誤要顯示的文字：前端字典有 errors.<code> 就用它（同一個 code 在三種語言一致），
 * 否則用後端依 Accept-Language 產生的 message；兩者都沒有時顯示通用訊息加 code。
 */
export function describeError(t: TFunction, err: unknown): string {
  const e = isApiError(err)
    ? err
    : new ApiError(0, CLIENT_CODES.badResponse, err instanceof Error ? err.message : "");
  const key = `errors.${e.code}`;
  const own = t(key, { defaultValue: "" });
  if (own) return own;
  if (e.message) return e.message;
  return t("errors.unknown", { code: e.code });
}
