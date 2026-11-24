// 總覽的系統告警（OPS-R13：「沒有任何 notifier 勾選 alert 時仍然記錄狀態，總覽照樣顯示」；NOTIFY-R8：
// 「標記 dropped 時寫入事件 notify.dropped（總覽顯示）」）。
//
//   - 告警讀 GET /api/system 的 alerts（OPS 介面：「總覽讀這兩個欄位」），不另外加到 /api/overview：
//     /api/system 只有 owner 能讀，告警訊息（排程錯誤、影音標題）不會因此開放給只有 read 的 token。
//   - 沒有送出的通知讀 GET /api/notifications?state=dropped（admin），只列最近 24 小時建立的，最多 5 則。
//   - 告警每 15 分鐘才檢查一次，兩個查詢各自 5 分鐘重讀，不跟著 SSE 失效；讀不到（403、501、其他錯誤）時整區隱藏，
//     總覽的其他區塊照常顯示。
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { formatDateTime, formatElapsed, formatNumber } from "../../i18n";

type Notification = components["schemas"]["Notification"];

const REFRESH_MS = 5 * 60_000;
/** 「最近」的沒有送出的通知：建立時間在這段時間內 */
export const DROPPED_WINDOW_MS = 24 * 3600_000;
const DROPPED_MAX = 5;
const SYSTEM_SECTION = { section: "system" } as const;
const NOTIFY_SECTION = { section: "notifications" } as const;

/** 最近 DROPPED_WINDOW_MS 內建立、已經 dropped 的通知，由新到舊 */
export function recentDropped(list: readonly Notification[], now: number): Notification[] {
  return list
    .filter(
      (n) => n.state === "dropped" && now - new Date(n.created_at).getTime() <= DROPPED_WINDOW_MS,
    )
    .sort((a, b) => b.created_at.localeCompare(a.created_at));
}

export function AlertsSection() {
  const { t } = useTranslation();
  const [now] = useState(() => Date.now());
  const system = useQuery({
    queryKey: queryKeys.system,
    queryFn: ({ signal }) => api.get("/api/system", { signal }),
    staleTime: REFRESH_MS,
    refetchInterval: REFRESH_MS,
    retry: false,
  });
  const dropped = useQuery({
    queryKey: [...queryKeys.notifications, "dropped"],
    queryFn: ({ signal }) => api.get("/api/notifications", { query: { state: "dropped" }, signal }),
    staleTime: REFRESH_MS,
    refetchInterval: REFRESH_MS,
    retry: false,
  });
  const alerts = system.data?.alerts ?? [];
  const drops = recentDropped(dropped.data?.notifications ?? [], now);
  if (alerts.length === 0 && drops.length === 0) return null;
  return (
    <section className="ov-sec" aria-labelledby="ov-alerts">
      <div className="ov-sec__head">
        <h2 id="ov-alerts">{t("overview.alerts.title")}</h2>
        <Link to="/settings/$section" params={SYSTEM_SECTION}>
          {t("overview.alerts.settings")}
        </Link>
      </div>
      {alerts.length > 0 && (
        <ul className="ov-alerts">
          {alerts.map((a) => (
            <li key={`${a.kind}:${a.key}`} className="ov-alert" data-category="needs_action">
              <span className="ov-alert__kind">
                {t(`settings.ops.kind.${a.kind}`, { defaultValue: a.kind })}
              </span>
              <span>{a.message}</span>
              <span className="secondary num">
                {t("settings.ops.since", {
                  time: formatDateTime(a.since),
                  elapsed: formatElapsed(now - new Date(a.since).getTime()),
                })}
              </span>
            </li>
          ))}
        </ul>
      )}
      {drops.length > 0 && (
        <div className="ov-dropped">
          <h3 className="ov-dropped__title">
            {t("overview.alerts.dropped", {
              count: drops.length,
              formatted: formatNumber(drops.length),
            })}
          </h3>
          <ul className="ov-events">
            {drops.slice(0, DROPPED_MAX).map((n) => (
              <li key={n.id}>
                <time className="num secondary" dateTime={n.created_at}>
                  {formatDateTime(n.created_at)}
                </time>
                <span>
                  {t("overview.alerts.droppedLine", {
                    notifier: n.notifier,
                    event: t(`settings.notify.event.${n.event}`, { defaultValue: n.event }),
                  })}
                  {n.last_error ? ` ${n.last_error}` : ""}
                </span>
              </li>
            ))}
          </ul>
          <Link to="/settings/$section" params={NOTIFY_SECTION}>
            {t("overview.alerts.notifySettings")}
          </Link>
        </div>
      )}
    </section>
  );
}
