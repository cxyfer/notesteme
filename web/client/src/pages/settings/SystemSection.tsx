// 系統（pages.md §12、OPS）：外部工具的路徑、狀態與版本、更新 yt-dlp 與回到上一個版本、資料目錄。
// 程式、schema 與工具的版本來自 GET /api/system（OPS-R22、NFR-OBS-3；工具的版本不知道時省略）；
// 磁碟用量讀總覽的 GET /api/overview（LIB-R22，和總覽頁共用 query），讀不到時只在那一行說明。
// 媒體檔保留天數不在 M4（ADR-0021 第 8 點），仍標示尚未提供。
// UI-5：最上面加上告警與備份（OPS，OpsStatus.tsx），最下面是匯入 bundle（BND，BundleImport.tsx），都是 lazy 載入。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, useToast } from "../../components";
import { formatBytes, formatDateTime } from "../../i18n";
import { browserTimeZone, fetchOverview } from "../overview/overviewQuery";
import { Loading, NotAvailable, Panel, TableScroll } from "./shared";

type Result = components["schemas"]["Result"];

const OpsStatus = lazy(() => import("./OpsStatus").then((m) => ({ default: m.OpsStatus })));
const BundleImport = lazy(() =>
  import("./BundleImport").then((m) => ({ default: m.BundleImport })),
);

export function SystemSection() {
  const { t } = useTranslation();
  const query = useQuery({ queryKey: queryKeys.system, queryFn: () => api.get("/api/system") });
  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorCallout
        error={query.error}
        actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
      />
    );
  const sys = query.data;
  const tools = Object.entries(sys.tools).sort(([a], [b]) => a.localeCompare(b));
  return (
    <>
      <Suspense fallback={<Loading />}>
        <OpsStatus alerts={sys.alerts} backup={sys.backup} />
      </Suspense>
      <Panel title={t("settings.system.toolsTitle")}>
        <TableScroll label={t("settings.system.toolsTitle")}>
          <table className="settings-table">
            <thead>
              <tr>
                <th scope="col">{t("settings.system.col.tool")}</th>
                <th scope="col">{t("settings.system.col.state")}</th>
                <th scope="col">{t("settings.system.col.version")}</th>
                <th scope="col">{t("settings.system.col.path")}</th>
                <th scope="col">{t("settings.system.col.source")}</th>
              </tr>
            </thead>
            <tbody>
              {tools.map(([name, tool]) => (
                <tr key={name}>
                  <th scope="row">{name}</th>
                  <td>
                    {tool.available
                      ? t("settings.system.available")
                      : t("settings.system.unavailable", { reason: tool.error ?? "" })}
                  </td>
                  <td className="num">{tool.version ?? t("settings.system.versionUnknown")}</td>
                  <td>
                    <code>{tool.path || t("settings.system.notFound")}</code>
                  </td>
                  <td>
                    {t(`settings.system.source.${tool.source}`, { defaultValue: tool.source })}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      </Panel>
      <YtDlp
        pinned={sys.yt_dlp.pinned}
        previous={sys.yt_dlp.previous}
        stateError={sys.yt_dlp.error}
      />
      <Panel title={t("settings.system.dataTitle")}>
        <dl className="settings-dl">
          <dt>{t("settings.system.appVersion")}</dt>
          <dd className="num">{sys.version}</dd>
          <dt>{t("settings.system.schemaVersion")}</dt>
          <dd className="num">{sys.schema_version ?? t("settings.system.versionUnknown")}</dd>
          <dt>{t("settings.system.data")}</dt>
          <dd>
            <code>{sys.data}</code>{" "}
            {t(`settings.system.source.${sys.data_source}`, { defaultValue: sys.data_source })}
          </dd>
          <dt>{t("settings.system.listen")}</dt>
          <dd>
            <code>{sys.listen}</code>{" "}
            {t(`settings.system.source.${sys.listen_source}`, { defaultValue: sys.listen_source })}
          </dd>
          <dt>{t("settings.system.disk")}</dt>
          <dd>
            <DiskUsage />
          </dd>
        </dl>
        <NotAvailable>{t("settings.system.retentionUnavailable")}</NotAvailable>
      </Panel>
      <Suspense fallback={<Loading />}>
        <BundleImport />
      </Suspense>
    </>
  );
}

/** 磁碟用量（LIB-R22）：和總覽頁共用 GET /api/overview 的 query；讀不到時只在這一行說明，不擋整個分區。 */
function DiskUsage() {
  const { t } = useTranslation();
  const tz = browserTimeZone();
  const q = useQuery({
    queryKey: queryKeys.overview(tz),
    queryFn: ({ signal }) => fetchOverview(tz, signal),
    retry: false,
  });
  if (q.isPending) return <span className="secondary">{t("common.loading")}</span>;
  const disk = q.data?.disk;
  if (q.isError || !disk)
    return (
      <span className="secondary">
        {t("settings.system.diskError", { reason: describeError(t, q.error) })}
      </span>
    );
  if (disk.data_bytes == null)
    return <span className="secondary">{t("settings.system.diskUnknown")}</span>;
  const used = formatBytes(disk.data_bytes);
  return (
    <span className="num">
      {disk.free_bytes == null
        ? t("settings.system.diskUsed", { used })
        : t("settings.system.diskUsedFree", { used, free: formatBytes(disk.free_bytes) })}
      {disk.measured_at && (
        <span className="secondary">
          {" "}
          {t("settings.system.diskMeasuredAt", { at: formatDateTime(disk.measured_at) })}
        </span>
      )}
    </span>
  );
}

function YtDlp({
  pinned,
  previous,
  stateError,
}: {
  pinned: string | null;
  previous: string | null;
  stateError?: string;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [pending, setPending] = useState<"update" | "rollback">();
  const [error, setError] = useState<unknown>();
  const [result, setResult] = useState<{ kind: "update" | "rollback"; r: Result }>();

  async function run(kind: "update" | "rollback") {
    setPending(kind);
    setError(undefined);
    try {
      const out =
        kind === "update"
          ? await api.post("/api/tools/yt-dlp/update", { body: {} })
          : await api.post("/api/tools/yt-dlp/rollback");
      setResult({ kind, r: out });
      toast.show({ title: resultText(t, kind, out) });
      await qc.invalidateQueries({ queryKey: queryKeys.system });
    } catch (err) {
      setError(err);
    } finally {
      setPending(undefined);
    }
  }

  const onUpdate = () => void run("update");
  const onRollback = () => void run("rollback");

  return (
    <Panel title={t("settings.system.ytdlpTitle")} description={t("settings.system.ytdlpHint")}>
      <dl className="settings-dl">
        <dt>{t("settings.system.pinned")}</dt>
        <dd className="num">{pinned ?? t("settings.system.notPinned")}</dd>
        <dt>{t("settings.system.previous")}</dt>
        <dd className="num">{previous ?? t("settings.system.noPrevious")}</dd>
      </dl>
      {stateError && <p className="settings-note">{stateError}</p>}
      <div className="settings-actions">
        <Button variant="primary" disabled={pending !== undefined} onClick={onUpdate}>
          {pending === "update" ? t("settings.system.updating") : t("settings.system.update")}
        </Button>
        <Button disabled={pending !== undefined || !previous} onClick={onRollback}>
          {t("settings.system.rollback")}
        </Button>
      </div>
      {result && (
        <p className="settings-note" data-kind={result.r.rolled_back ? undefined : "ok"}>
          {resultText(t, result.kind, result.r)}
        </p>
      )}
      {error !== undefined && <ErrorCallout error={error} />}
    </Panel>
  );
}

function resultText(
  t: (k: string, o?: Record<string, unknown>) => string,
  kind: "update" | "rollback",
  r: Result,
): string {
  if (kind === "rollback") return t("settings.system.rolledBackTo", { version: r.version });
  if (r.skipped) return t("settings.system.skipped", { version: r.version, reason: r.message });
  if (r.rolled_back)
    return t("settings.system.updateFailed", { version: r.version, reason: r.message });
  return t("settings.system.updated", { version: r.version });
}
