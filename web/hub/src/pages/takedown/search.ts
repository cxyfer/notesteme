// 下架申請表單的 URL 參數：?version= 預先填入版本 ID。
export function validateTakedownSearch(s: Record<string, unknown>): { version?: string } {
  return typeof s.version === "string" && /^[\w-]{1,64}$/.test(s.version)
    ? { version: s.version }
    : {};
}
