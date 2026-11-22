// API 錯誤：後端一律回 { code, message, ... }（internal/api/errors.go）。
// 程式只依 code 判斷（NFR-I18N-2）；message 是後端依 Accept-Language 產生的說明，只用來顯示。
import type { components } from "./schema";

export type ApiErrorBody = components["schemas"]["Error"];

/** 前端自己產生的 code（後端不會回這些）。 */
export const CLIENT_CODES = {
  network: "network_error",
  badResponse: "bad_response",
} as const;

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly body: Partial<ApiErrorBody>;

  constructor(status: number, code: string, message: string, body: Partial<ApiErrorBody> = {}) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** 從非 2xx 的回應建立 ApiError；本體不是預期的格式時 code 是 bad_response。 */
export async function errorFromResponse(res: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = undefined;
  }
  if (body && typeof body === "object" && typeof (body as { code?: unknown }).code === "string") {
    const b = body as ApiErrorBody;
    return new ApiError(res.status, b.code, typeof b.message === "string" ? b.message : "", b);
  }
  return new ApiError(res.status, CLIENT_CODES.badResponse, res.statusText);
}
