// 系統的告警與備份（OPS-R11～R17）：讀 GET /api/system 的 alerts、backup。由 SystemSection lazy 載入。
//
// API 只列出「目前成立」的告警：恢復的告警從清單消失（並依通知設定送出解除通知），
// 沒有「已恢復」的歷史，所以這裡只顯示目前的狀態（見 UI README 10.15）。
import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { components } from "../../api/schema";
import { formatDateTime, formatElapsed } from "../../i18n";
import { Panel } from "./shared";

type Alert = components["schemas"]["AlertView"];
type Backup = components["schemas"]["BackupView"];

export function OpsStatus({ alerts, backup }: { alerts: Alert[] | null; backup: Backup }) {
  const { t } = useTranslation();
  // 持續時間以掛載（讀到資料）的時間計算；資料重新讀取時 SystemSection 會重新渲染
  const [now] = useState(() => Date.now());
  const list = alerts ?? [];
  return (
    <>
      <Panel title={t("settings.ops.alertsTitle")} description={t("settings.ops.alertsHint")}>
        {list.length === 0 ? (
          <p className="settings-note" data-kind="ok">
            {t("settings.ops.noAlerts")}
          </p>
        ) : (
          <ul className="settings-rows">
            {list.map((a) => (
              <li key={`${a.kind}:${a.key}`} className="settings-row">
                <div className="settings-row__main">
                  <span className="settings-row__name">
                    {t(`settings.ops.kind.${a.kind}`, { defaultValue: a.kind })}
                  </span>
                  <span>{a.message}</span>
                  <span className="settings-row__meta">
                    {t("settings.ops.since", {
                      time: formatDateTime(a.since),
                      elapsed: formatElapsed(now - new Date(a.since).getTime()),
                    })}
                    {a.key && (
                      <>
                        {" · "}
                        <code>{a.key}</code>
                      </>
                    )}
                  </span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
      <Panel title={t("settings.ops.backupTitle")} description={t("settings.ops.backupHint")}>
        <dl className="settings-dl">
          <dt>{t("settings.ops.lastOk")}</dt>
          <dd>{backup.last_ok_at ? formatDateTime(backup.last_ok_at) : t("settings.ops.never")}</dd>
          <dt>{t("settings.ops.nextRun")}</dt>
          <dd>
            {backup.next_run_at ? formatDateTime(backup.next_run_at) : t("settings.ops.disabled")}
          </dd>
        </dl>
        {backup.last_error ? (
          <div className="callout" role="status" data-category="needs_action">
            <p className="callout__category">{t("settings.ops.lastFailed")}</p>
            <p>{backup.last_error}</p>
          </div>
        ) : (
          backup.last_ok_at && (
            <p className="settings-note" data-kind="ok">
              {t("settings.ops.backupOk")}
            </p>
          )
        )}
      </Panel>
    </>
  );
}
