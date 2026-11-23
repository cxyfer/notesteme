// 一個版本的摘要列（瀏覽結果、影片頁）：分數、授權、上傳者、模型與「未驗證的模型」標示（HREP-R16、R17）。
// 文案：model_verified 為 true 時寫「由 Client 記錄的模型」，不寫「已驗證」（ADR-0031 第 3 點）。
// 使用者上傳的文字（模型名稱、上傳者名稱）一律是文字節點。
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import type { VersionItem } from "../../api/types";
import { formatDateTime, formatNumber } from "../../i18n";
import { kindLabel } from "../versionText";

export function ModelLine({
  v,
}: {
  v: Pick<VersionItem, "transcribe_model" | "correct_model" | "translate_model" | "model_verified">;
}) {
  const { t } = useTranslation();
  const models = (
    [
      ["transcribe", v.transcribe_model],
      ["correct", v.correct_model],
      ["translate", v.translate_model],
    ] as const
  ).filter(([, m]) => m);
  return (
    <dl className="models">
      {models.map(([k, m]) => (
        <div key={k}>
          <dt>{t(`model.${k}`)}</dt>
          <dd>{m}</dd>
        </div>
      ))}
      <div>
        <dt className="visually-hidden">{t("model.status")}</dt>
        <dd>
          {v.model_verified ? (
            <span className="secondary">{t("model.recorded")}</span>
          ) : (
            <span className="badge badge--warn">{t("model.unverified")}</span>
          )}
        </dd>
      </div>
    </dl>
  );
}

export function VersionSummary({ v, extra }: { v: VersionItem; extra?: ReactNode }) {
  const { t } = useTranslation();
  const uploaders = (v.uploaders ?? []).map((u) => u.display_name);
  return (
    <article className="version-row">
      <div className="version-row__score num">
        <span aria-hidden="true">{formatNumber(v.score)}</span>
        <span className="visually-hidden">
          {t("version.scoreLabel", { score: formatNumber(v.score) })}
        </span>
      </div>
      <div className="version-row__body">
        <h3 className="version-row__title">
          <Link to="/versions/$id" params={{ id: v.id }}>
            {kindLabel(t, v.kind, v.variant, v.lang)}
          </Link>
        </h3>
        <ModelLine v={v} />
        <p className="version-row__meta secondary">
          <span>{t("version.license", { license: v.license })}</span>
          <span>
            {t("version.uploaders", {
              names: uploaders.length ? uploaders.join(t("common.listSep")) : t("common.none"),
            })}
          </span>
          <span>{formatDateTime(v.created_at)}</span>
        </p>
      </div>
      {extra ? <div className="version-row__extra">{extra}</div> : null}
    </article>
  );
}
