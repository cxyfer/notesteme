// 版本頁（HREP-R6、R17、R18，HUB-06）：來源紀錄、衍生關係（「以 @alice 的原始字幕重新生成」）、下載、預覽、
// 投票、檢舉、下架申請的連結；有 moderate 時可以隱藏。上傳的內容一律是不可信的文字：筆記用 web/shared 的
// NoteMarkdown（不允許原始 HTML），字幕與來源紀錄以文字節點顯示。
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { hasPermission, type RelatedView, type VersionView } from "../../api/types";
import { CopyButton, ErrorCallout, Spinner } from "../../components";
import { formatDateTime, formatNumber } from "../../i18n";
import { useMe } from "../../shell/useMe";
import { usePageTitle } from "../../shell/usePageTitle";
import { downloadPath } from "../shared/cues";
import { ModelLine } from "../shared/VersionSummary";
import { kindLabel, videoLabel, videoLink } from "../versionText";
import { Community } from "./Community";
import { Preview } from "./Preview";

/** 每種產出可以下載的格式（HREP-R18；frames 沒有可下載的格式） */
export const FORMATS: Record<string, readonly string[]> = {
  transcript: ["srt", "vtt", "txt", "cues-json"],
  note: ["md"],
  chapters: ["json"],
};

function Related({ title, items }: { title: string; items: readonly RelatedView[] }) {
  const { t } = useTranslation();
  if (items.length === 0) return null;
  return (
    <section className="related">
      <h3>{title}</h3>
      <ul>
        {items.map((r) => (
          <li key={`${r.relation}-${r.id}`}>
            <Link to="/versions/$id" params={{ id: r.id }}>
              {kindLabel(t, r.kind, r.variant, r.lang)}
            </Link>{" "}
            <span className="secondary">
              {r.relation === "regenerated_from"
                ? t("version.regeneratedFrom", { name: r.uploader })
                : t("version.derivedBy", { name: r.uploader })}
            </span>
            {r.state !== "visible" && (
              <span className="badge">{t(`state.${r.state}`, { defaultValue: r.state })}</span>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function ProvenanceList({ data }: { data: Record<string, unknown> }) {
  const { t } = useTranslation();
  const entries = Object.entries(data ?? {});
  if (entries.length === 0) return <p className="secondary">{t("version.noProvenance")}</p>;
  return (
    <dl className="provenance">
      {entries.map(([k, v]) => (
        <div key={k}>
          <dt className="mono">{k}</dt>
          <dd>
            <code>{typeof v === "string" ? v : JSON.stringify(v)}</code>
          </dd>
        </div>
      ))}
    </dl>
  );
}

export function VersionPage() {
  const { t } = useTranslation();
  const { id } = useParams({ from: "/versions/$id" });
  const me = useMe();
  const q = useQuery({
    queryKey: queryKeys.version(id),
    queryFn: ({ signal }) =>
      api.get<"/api/v1/versions/{id}">(apiPath("/api/v1/versions/{id}", { id }), { signal }),
  });
  const v: VersionView | undefined = q.data;
  usePageTitle(v ? kindLabel(t, v.kind, v.variant, v.lang) : t("version.title"));

  if (q.isPending) return <Spinner showLabel />;
  if (q.isError || !v)
    return (
      <section>
        <h1 className="page__title">{t("version.title")}</h1>
        <ErrorCallout error={q.error} />
      </section>
    );

  const canDownload = hasPermission(me.data, "download");
  const formats = FORMATS[v.kind] ?? [];
  const raw = v.kind === "transcript" && (v.variant === "source" || v.variant === "normalized");
  const uploaders = (v.uploaders ?? []).map((u) => u.display_name);

  return (
    <article aria-labelledby="version-title" className="version-page">
      <p className="breadcrumb">
        <Link {...videoLink(v.video)}>
          {t("version.backToVideo", { video: videoLabel(v.video) })}
        </Link>
      </p>
      <h1 id="version-title" className="page__title">
        {kindLabel(t, v.kind, v.variant, v.lang)}
      </h1>
      {v.state === "hidden" && <p className="notice">{t("version.hiddenNotice")}</p>}

      <div className="version-page__grid">
        <div>
          <dl className="facts">
            <div>
              <dt>{t("version.score")}</dt>
              <dd className="num">{formatNumber(v.score)}</dd>
            </div>
            <div>
              <dt>{t("version.licenseLabel")}</dt>
              <dd>{v.license}</dd>
            </div>
            <div>
              <dt>{t("version.uploadersLabel")}</dt>
              <dd>{uploaders.length ? uploaders.join(t("common.listSep")) : t("common.none")}</dd>
            </div>
            <div>
              <dt>{t("version.visibility")}</dt>
              <dd>
                {t(`visibility.${v.visibility.split(":")[0]}`, {
                  defaultValue: v.visibility,
                  group: v.visibility.split(":")[1] ?? "",
                })}
              </dd>
            </div>
            <div>
              <dt>{t("version.created")}</dt>
              <dd>{formatDateTime(v.created_at)}</dd>
            </div>
            <div>
              <dt>{t("version.sha256")}</dt>
              <dd className="mono break">{v.sha256}</dd>
            </div>
          </dl>
          <ModelLine v={v} />
          <Related title={t("version.parents")} items={v.parents ?? []} />
          <Related title={t("version.children")} items={v.children ?? []} />
        </div>
        <aside className="version-page__side" aria-label={t("version.actions")}>
          <section>
            <h2>{t("version.download")}</h2>
            {formats.length === 0 ? (
              <p className="secondary">{t("version.noDownload")}</p>
            ) : canDownload ? (
              <ul className="downloads">
                {formats.map((f) => (
                  <li key={f}>
                    <a
                      className="button button--outline"
                      href={`${downloadPath(v.id)}?format=${encodeURIComponent(f)}`}
                      download
                    >
                      {t("version.downloadAs", { format: f.toUpperCase() })}
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="secondary">{t("version.needDownload")}</p>
            )}
          </section>
          <Community version={v} />
          {raw && (
            <section>
              <h2>{t("version.regenerateTitle")}</h2>
              <p className="secondary">{t("version.regenerateHint")}</p>
              <p className="mono break">{v.id}</p>
              <CopyButton text={v.id} label={t("version.copyId")} />
            </section>
          )}
        </aside>
      </div>

      <section className="version-page__preview">
        <h2>{t("version.preview")}</h2>
        {canDownload ? (
          <Preview version={v} />
        ) : (
          <p className="secondary">{t("version.needDownload")}</p>
        )}
      </section>

      <details className="version-page__provenance">
        <summary>{t("version.provenance")}</summary>
        <ProvenanceList data={v.provenance as Record<string, unknown>} />
      </details>
    </article>
  );
}
