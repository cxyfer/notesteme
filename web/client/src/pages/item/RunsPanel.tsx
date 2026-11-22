// 執行紀錄（PIPE-R13、pages.md §4「紀錄」）：每筆 stage run 的狀態、時間、執行方式與當時生效的設定。
// API 沒有 log 片段與花費（見 docs/ui/README.md 10.7 的缺口），設定快照用純文字的 JSON 顯示。
import { useTranslation } from "react-i18next";

import { ErrorCallout, Spinner } from "../../components";
import { formatDateTime } from "../../i18n";
import { stageConfig, type ApiRun } from "./data";

function duration(run: ApiRun): string | undefined {
  if (!run.finished_at) return undefined;
  const ms = new Date(run.finished_at).getTime() - new Date(run.started_at).getTime();
  if (!Number.isFinite(ms) || ms < 0) return undefined;
  const s = Math.round(ms / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

export function RunsPanel({
  runs,
  isPending,
  error,
  onRetry,
}: {
  runs: readonly ApiRun[] | undefined;
  isPending: boolean;
  error: unknown;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  if (isPending) return <Spinner label={t("item.runs.loading")} showLabel />;
  if (error && !runs)
    return (
      <ErrorCallout error={error} actions={[{ label: t("common.retry"), onClick: onRetry }]} />
    );
  if (!runs?.length) return <p className="secondary">{t("item.runs.empty")}</p>;
  return (
    <ol className="runs" aria-label={t("item.runs.title")}>
      {runs.map((r) => {
        const cfg = stageConfig(r);
        const d = duration(r);
        return (
          <li key={r.id} className="run" data-status={r.status}>
            <p className="run__head">
              <strong>{t(`stage.${r.stage}`, { defaultValue: r.stage })}</strong>
              <span>{t(`stageStatus.${r.status}`, { defaultValue: r.status })}</span>
              <span className="secondary num">{formatDateTime(r.started_at)}</span>
              {d && <span className="secondary num">{t("item.runs.took", { duration: d })}</span>}
            </p>
            <p className="run__meta secondary">
              {[
                cfg.executor && t(`executor.${cfg.executor}`, { defaultValue: cfg.executor }),
                cfg.provider,
                cfg.model,
                cfg.prompt && t("item.runs.prompt", { prompt: cfg.prompt }),
                t("item.runs.impl", { version: r.impl_version }),
              ]
                .filter(Boolean)
                .join(t("common.listSeparator"))}
            </p>
            <details className="run__config">
              <summary>{t("item.runs.config")}</summary>
              <pre className="run__json">{JSON.stringify(r.config, null, 2)}</pre>
            </details>
          </li>
        );
      })}
    </ol>
  );
}
