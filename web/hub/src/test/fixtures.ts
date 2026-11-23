// Hub API 的測試資料（形狀依 internal/hub/hubapi/openapi.json）。
import type {
  AdminUserView,
  Me,
  MyShareView,
  ReportView,
  TakedownView,
  VersionItem,
  VersionView,
} from "../api/types";
import { json, type MockHandler } from "./mockApi";

export const member: Me = {
  id: "u1",
  display_name: "alice",
  groups: ["members"],
  identities: [{ provider: "github", login: "alice" }],
  permissions: ["read", "download"],
};
export const contributor: Me = {
  ...member,
  groups: ["members", "contributors"],
  permissions: ["read", "download", "regenerate", "upload"],
};
export const moderator: Me = {
  ...member,
  id: "u9",
  display_name: "mod",
  groups: ["members", "moderators"],
  permissions: ["read", "download", "moderate"],
};
export const admin: Me = {
  ...member,
  id: "u0",
  display_name: "root",
  groups: ["members", "admins"],
  permissions: ["read", "download", "regenerate", "upload", "moderate", "admin"],
};

export const providers = {
  providers: [
    { id: "google", name: "Google" },
    { id: "github", name: "GitHub" },
    { id: "linuxdo", name: "Linux.do" },
  ],
};

export const item: VersionItem = {
  id: "v1",
  video: { platform: "youtube", external_id: "abc123", part: null },
  kind: "transcript",
  variant: "corrected",
  lang: "en",
  transcribe_model: "youtube-manual",
  correct_model: "anthropic/claude-sonnet",
  translate_model: "",
  model_verified: true,
  license: "CC-BY-4.0",
  score: 7,
  sha256: "a".repeat(64),
  uploaders: [{ display_name: "alice" }],
  created_at: "2026-11-20T10:00:00Z",
};
export const unverified: VersionItem = {
  ...item,
  id: "v2",
  correct_model: "agent/whatever",
  model_verified: false,
  badges: ["unverified_model"],
  score: 2,
  uploaders: [{ display_name: "bob" }],
};

export const version: VersionView = {
  ...item,
  provenance: { source: "youtube-manual", executor: "api", parents_sha256: ["b".repeat(64)] },
  parents: [
    {
      id: "v0",
      relation: "regenerated_from",
      kind: "transcript",
      variant: "source",
      lang: "en",
      uploader: "carol",
      state: "visible",
      summary: "以 @carol 的原始字幕重新生成",
    },
  ],
  children: [],
  visibility: "authenticated",
  state: "visible",
};

export const videoPage = {
  video: {
    platform: "youtube",
    external_id: "abc123",
    part: null,
    title: "<b>Talk</b>",
    channel_external_id: "UC1",
    channel_title: "Chan",
  },
  groups: [{ kind: "transcript", variant: "corrected", lang: "en", versions: [item, unverified] }],
};

export const cuesA = JSON.stringify({
  lang: "en",
  variant: "corrected",
  speakers: [],
  cues: [
    { index: 1, start_ms: 0, end_ms: 2000, text: "Hello world" },
    { index: 2, start_ms: 2000, end_ms: 4000, text: "Second line" },
  ],
});
export const cuesB = JSON.stringify({
  lang: "en",
  variant: "corrected",
  speakers: [],
  cues: [
    { index: 1, start_ms: 0, end_ms: 2000, text: "Hello there world" },
    { index: 2, start_ms: 2000, end_ms: 4000, text: "<img src=x onerror=alert(1)>" },
  ],
});

export const report: ReportView = {
  id: "r1",
  version_id: "v1",
  reason: "bad_quality",
  note: "<script>alert(1)</script>",
  reporter: { id: "u2", display_name: "bob" },
  state: "open",
  created_at: "2026-11-21T10:00:00Z",
  decided_at: null,
};
export const takedown: TakedownView = {
  id: "t1",
  version_id: "v1",
  reason: "copyright",
  statement: "This is my work and was shared without permission.",
  contact_email: "owner@example.org",
  state: "open",
  note: "",
  created_at: "2026-11-21T11:00:00Z",
  decided_at: null,
};
export const users: AdminUserView[] = [
  {
    id: "u1",
    display_name: "alice",
    created_at: "2026-11-01T00:00:00Z",
    groups: ["members", "contributors"],
    identities: [{ provider: "github", login: "alice" }],
    suspended_at: null,
    suspended_reason: "",
  },
  {
    id: "u2",
    display_name: "bob",
    created_at: "2026-11-02T00:00:00Z",
    groups: ["members"],
    identities: [{ provider: "google", login: "bob@example.org" }],
    suspended_at: "2026-11-22T00:00:00Z",
    suspended_reason: "spam",
  },
];
export const shares: MyShareView[] = [
  {
    id: "s1",
    video: item.video,
    created_at: "2026-11-20T10:00:00Z",
    withdrawn_at: null,
    versions: [
      { ...item, visibility: "public", state: "visible" },
      {
        ...item,
        id: "v3",
        kind: "note",
        variant: "",
        state: "removed",
        visibility: "public",
        removal: { source: "takedown", reason: "copyright" },
      },
    ],
  },
];

/** 依身分回應 /me（null 是未登入的 401） */
export function meHandler(me: Me | null): MockHandler {
  return () => (me ? me : json(401, { code: "unauthorized", message: "請先登入 Hub。" }));
}
