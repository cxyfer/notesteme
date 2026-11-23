// 系統（pages.md §12、OPS）：外部工具的路徑與狀態、更新 yt-dlp 與回到上一個版本、資料目錄。
// GET /api/system 沒有工具的版本號、磁碟用量與媒體檔保留天數；版本號只能從更新的結果得知，其他標示尚未提供。
// UI-5：最上面加上告警與備份（OPS，OpsStatus.tsx），最下面是匯入 bundle（BND，BundleImport.tsx），都是 lazy 載入。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, useToast } from "../../components";
import { Loading, NotAvailable, Panel } from "./shared";

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
        <div className="settings-table-wrap">
          <table className="settings-table">
            <thead>
              <tr>
                <th scope="col">{t("settings.system.col.tool")}</th>
                <th scope="col">{t("settings.system.col.state")}</th>
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
        </div>
        <NotAvailable>{t("settings.system.versionsUnavailable")}</NotAvailable>
      </Panel>
      <YtDlp
        pinned={sys.yt_dlp.pinned}
        previous={sys.yt_dlp.previous}
        stateError={sys.yt_dlp.error}
      />
      <Panel title={t("settings.system.dataTitle")}>
        <dl className="settings-dl">
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
        </dl>
        <NotAvailable>{t("settings.system.diskUnavailable")}</NotAvailable>
        <NotAvailable>{t("settings.system.retentionUnavailable")}</NotAvailable>
      </Panel>
      <Suspense fallback={<Loading />}>
        <BundleImport />
      </Suspense>
    </>
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
