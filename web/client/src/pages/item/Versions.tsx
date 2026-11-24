// 產出的版本與比較（pages.md §4「紀錄」：每個產出都有版本，可以比較兩個版本；LIB-R11、LIB-R25）：
//   - GET /api/items/{id}/artifacts 列出每個產出保留中的版本，依（種類、變體、語言）分組。
//   - 字幕與筆記可以選兩個版本比較：GET /api/artifacts/{id}/content（版本不會變，快取不過期）。
//   - 字幕依時間重疊分組後逐組逐字比對（同字幕分頁的「差異」，不把整份字幕接成一串，避免超過 LCS 的上限）；
//     筆記先逐行比對，相鄰的刪除與新增行再逐字比對。
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { DiffText, diffChars, hasChanges, type DiffPart } from "@shared";

import { api, apiPath, requestBlob } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, SelectField, Spinner } from "../../components";
import { formatDateTime, formatTimecode } from "../../i18n";
import { alignCues, joinText, toCues } from "./cues";

type ApiVersion = components["schemas"]["ArtifactVersionBody"];

/** 可以讀取內容、比較的種類（LIB-R25） */
const COMPARABLE = new Set(["transcript", "note"]);
/** 最多列出幾處差異（上千句的字幕全部不同時不卡住畫面） */
const MAX_CHANGES = 200;

interface VersionGroup {
  key: string;
  kind: string;
  variant?: string;
  lang?: string;
  /** 由新到舊 */
  versions: ApiVersion[];
}

export function groupVersions(list: readonly ApiVersion[]): VersionGroup[] {
  const out: VersionGroup[] = [];
  const byKey = new Map<string, VersionGroup>();
  for (const v of list) {
    const key = `${v.kind}|${v.variant ?? ""}|${v.lang ?? ""}`;
    let g = byKey.get(key);
    if (!g) {
      g = { key, kind: v.kind, variant: v.variant, lang: v.lang, versions: [] };
      byKey.set(key, g);
      out.push(g);
    }
    g.versions.push(v);
  }
  return out;
}

export function useArtifacts(itemId: string) {
  return useQuery({
    queryKey: queryKeys.items.artifacts(itemId),
    queryFn: ({ signal }) =>
      api.get<"/api/items/{id}/artifacts">(apiPath("/api/items/{id}/artifacts", { id: itemId }), {
        signal,
      }),
  });
}

export function VersionsSection({ itemId }: { itemId: string }) {
  const { t } = useTranslation();
  const q = useArtifacts(itemId);
  const groups = useMemo(() => groupVersions(q.data?.artifacts ?? []), [q.data]);
  return (
    <section className="versions" aria-labelledby="versions-title">
      <h2 id="versions-title" className="section__title">
        {t("item.versions.title")}
      </h2>
      <p className="secondary">{t("item.versions.description")}</p>
      {q.isPending ? (
        <Spinner label={t("item.versions.loading")} showLabel />
      ) : q.error && !q.data ? (
        <ErrorCallout
          error={q.error}
          actions={[{ label: t("common.retry"), onClick: () => void q.refetch() }]}
        />
      ) : !groups.length ? (
        <p className="secondary">{t("item.versions.empty")}</p>
      ) : (
        <ul className="versions__groups">
          {groups.map((g) => (
            <li key={g.key}>
              <GroupView group={g} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function useGroupName() {
  const { t } = useTranslation();
  return (g: { kind: string; variant?: string; lang?: string }) =>
    [
      t(`item.versions.kinds.${g.kind}`, { defaultValue: g.kind }),
      g.kind === "transcript" && g.variant
        ? t(`item.cues.modes.${g.variant}`, { defaultValue: g.variant })
        : "",
      g.lang ?? "",
    ]
      .filter(Boolean)
      .join(t("common.listSeparator"));
}

function versionLabel(t: ReturnType<typeof useTranslation>["t"], v: ApiVersion) {
  return [
    formatDateTime(v.created_at),
    v.current ? t("item.versions.current") : "",
    v.executor ? t(`executor.${v.executor}`, { defaultValue: v.executor }) : "",
    v.provider,
    v.model,
    v.prompt && t("item.runs.prompt", { prompt: v.prompt }),
  ]
    .filter(Boolean)
    .join(t("common.listSeparator"));
}

function GroupView({ group }: { group: VersionGroup }) {
  const { t } = useTranslation();
  const name = useGroupName()(group);
  const comparable = COMPARABLE.has(group.kind) && group.versions.length >= 2;
  // 預設比較「上一版」與「目前」
  const [older, setOlder] = useState(group.versions[1]?.id ?? "");
  const [newer, setNewer] = useState(group.versions[0]?.id ?? "");
  const [shown, setShown] = useState<{ a: string; b: string } | null>(null);
  const ids = group.versions.map((v) => v.id);
  const a = ids.includes(older) ? older : (ids[1] ?? "");
  const b = ids.includes(newer) ? newer : (ids[0] ?? "");
  const options = group.versions.map((v) => ({ value: v.id, label: versionLabel(t, v) }));
  return (
    <div className="versions__group">
      <h3 className="versions__name">
        {name}
        <span className="secondary">
          {" "}
          {t("item.versions.count", { count: group.versions.length })}
        </span>
      </h3>
      <ol className="versions__list">
        {group.versions.map((v) => (
          <li key={v.id} className="secondary num">
            {versionLabel(t, v)}
          </li>
        ))}
      </ol>
      {comparable && (
        <div className="versions__compare">
          <SelectField
            label={t("item.versions.before")}
            value={a}
            onChange={(e) => setOlder(e.target.value)}
            options={options}
          />
          <SelectField
            label={t("item.versions.after")}
            value={b}
            onChange={(e) => setNewer(e.target.value)}
            options={options}
          />
          <Button
            variant="outline"
            disabled={a === b}
            onClick={() => setShown({ a, b })}
            aria-label={t("item.versions.compareGroup", { name })}
          >
            {t("item.versions.compare")}
          </Button>
          {a === b && <p className="secondary">{t("item.versions.same")}</p>}
        </div>
      )}
      {shown && <VersionDiff kind={group.kind} lang={group.lang} a={shown.a} b={shown.b} />}
    </div>
  );
}

/** 一個版本的內容：字幕是 cues（json），筆記是 Markdown；版本不會變，所以不過期 */
function useVersionContent(kind: string, id: string) {
  return useQuery({
    queryKey: queryKeys.artifactContent(id),
    queryFn: async ({ signal }) => {
      const path = apiPath("/api/artifacts/{id}/content", { id });
      if (kind === "transcript") {
        const r = await api.get<"/api/artifacts/{id}/content">(path, { signal });
        return { cues: toCues(r?.cues), text: "" };
      }
      const blob = await requestBlob("GET", `${path}?format=md`, { signal });
      return { cues: [], text: await blob.text() };
    },
    staleTime: Infinity,
  });
}

function VersionDiff({ kind, lang, a, b }: { kind: string; lang?: string; a: string; b: string }) {
  const { t } = useTranslation();
  const before = useVersionContent(kind, a);
  const after = useVersionContent(kind, b);
  const labels = { del: t("diff.deleted"), ins: t("diff.inserted") };
  if (before.isPending || after.isPending)
    return <Spinner label={t("item.versions.loadingDiff")} showLabel />;
  const err = before.error ?? after.error;
  if (err || !before.data || !after.data)
    return (
      <ErrorCallout
        error={err}
        actions={[
          {
            label: t("common.retry"),
            onClick: () => {
              void before.refetch();
              void after.refetch();
            },
          },
        ]}
      />
    );
  const rows =
    kind === "transcript"
      ? alignCues(before.data.cues, after.data.cues)
          .map((g) => ({
            key: `${g.start}`,
            at: formatTimecode(g.start),
            parts: diffChars(joinText(g.a, lang), joinText(g.b, lang)),
          }))
          .filter((r) => hasChanges(r.parts))
      : diffLines(before.data.text, after.data.text);
  return (
    <div className="versions__diff">
      <p role="status">
        {rows.length
          ? t("item.versions.changes", { count: rows.length })
          : t("item.versions.noChanges")}
      </p>
      {rows.length > 0 && (
        <ol className="versions__changes">
          {rows.slice(0, MAX_CHANGES).map((r) => (
            <li key={r.key}>
              {/* 筆記沒有時間碼：仍保留空的欄，差異才會在第二欄 */}
              <span className="versions__at num">{r.at}</span>
              <DiffText parts={r.parts} labels={labels} lang={lang} />
            </li>
          ))}
        </ol>
      )}
      {rows.length > MAX_CHANGES && (
        <p className="secondary">{t("item.versions.truncated", { count: MAX_CHANGES })}</p>
      )}
    </div>
  );
}

/** Unicode 私用區：每個不同的行對應一個字元，逐行比對就能沿用逐字比對的 diffChars */
const PUA_START = 0xe000;
const PUA_SIZE = 0x1900;

/**
 * 筆記的逐行差異：先逐行比對，連續的刪除行與新增行合成一處，再逐字比對那一處；
 * 不同的行太多（超過私用區的大小）時整份當成一處。回傳有變動的地方。
 */
export function diffLines(before: string, after: string) {
  const a = before.split("\n");
  const b = after.split("\n");
  const codes = new Map<string, string>();
  const enc = (lines: string[]) =>
    lines
      .map((l) => {
        let c = codes.get(l);
        if (c === undefined) {
          c = String.fromCharCode(PUA_START + codes.size);
          codes.set(l, c);
        }
        return c;
      })
      .join("");
  const ea = enc(a);
  const eb = enc(b);
  if (codes.size > PUA_SIZE) {
    const parts = diffChars(before, after);
    return hasChanges(parts) ? [{ key: "all", at: "", parts }] : [];
  }
  const lines = [...codes.keys()];
  const dec = (s: string) => Array.from(s, (ch) => lines[ch.charCodeAt(0) - PUA_START]!);
  const out: { key: string; at: string; parts: DiffPart[] }[] = [];
  let del: string[] = [];
  let ins: string[] = [];
  let line = 1;
  let startLine = 1;
  const flush = () => {
    if (!del.length && !ins.length) return;
    out.push({
      key: `l${startLine}`,
      at: "",
      parts: diffChars(del.join("\n"), ins.join("\n")),
    });
    del = [];
    ins = [];
  };
  for (const p of diffChars(ea, eb)) {
    const ls = dec(p.text);
    if (p.type === "eq") {
      flush();
      line += ls.length;
      continue;
    }
    if (!del.length && !ins.length) startLine = line;
    if (p.type === "del") {
      del.push(...ls);
      line += ls.length;
    } else ins.push(...ls);
  }
  flush();
  return out;
}
