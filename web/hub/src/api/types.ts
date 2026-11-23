// OpenAPI 型別的別名（src/api/schema.d.ts 由 internal/hub/hubapi/openapi.json 產生）。
import type { components } from "./schema";

type S = components["schemas"];
export type Me = S["Me"];
export type Provider = S["Provider"];
export type VersionItem = S["VersionItem"];
export type VersionView = S["VersionView"];
export type VersionListView = S["VersionListView"];
export type VideoPageView = S["VideoPageView"];
export type VideoRef = S["VideoRef"];
export type RelatedView = S["RelatedView"];
export type TokenView = S["TokenView"];
export type CreateTokenOutput = S["CreateTokenOutputBody"];
export type MyShareView = S["MyShareView"];
export type MyVersionView = S["MyVersionView"];
export type ReportView = S["ReportView"];
export type TakedownView = S["TakedownView"];
export type AdminUserView = S["AdminUserView"];
export type AuditView = S["AuditView"];

/** 權限（HACC-R10），依 /me 回傳的順序 */
export const PERMISSIONS = [
  "read",
  "download",
  "regenerate",
  "upload",
  "moderate",
  "admin",
] as const;
export type Permission = (typeof PERMISSIONS)[number];

export function hasPermission(me: Me | null | undefined, p: Permission): boolean {
  const ps = me?.permissions ?? [];
  return ps.includes(p) || ps.includes("admin");
}
