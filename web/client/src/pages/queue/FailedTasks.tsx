// 佇列頁的「失敗與停住」（pages.md §6）：GET /api/queue 的 failed 清單，每列可以重試（POST /api/tasks/{id}/retry），
// 也可以全部重試（依序送出，一個失敗不影響其他）。被取消的步驟也列在這裡，重試就會接著處理。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, StatusBadge, useToast, type Status } from "../../components";
import { formatNumber } from "../../i18n";
import { useItemDetail } from "../library/LibraryRow";

/** 點列打開影音詳情的「紀錄」分頁（同任務列表） */
const RUNS_TAB = { tab: "runs" } as const;

export type FailedTask = components["schemas"]["QueueFailedTask"];

/** 步驟狀態對應到標籤：blocked 依原因，skipped 只會是被取消的 */
function badgeOf(f: FailedTask): Status {
  if (f.status === "failed") return "failed";
  if (f.status === "skipped") return "skipped";
  if (f.block_reason === "budget_blocked" || f.block_reason === "awaiting_user")
    return f.block_reason;
  return "needs_action";
}

export function FailedTasks({ failed }: { failed: FailedTask[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);

  const refresh = (itemID: string) => {
    void qc.invalidateQueries({ queryKey: queryKeys.queue });
    void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemID) });
  };
  const retry = useMutation({
    mutationFn: (f: FailedTask) => api.post(apiPath("/api/tasks/{id}/retry", { id: f.id })),
    onSuccess: (_d, f) => {
      refresh(f.item_id);
      toast.show({ title: t("queue.failed.retried") });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });

  // 依序送出：同時送出上百個請求沒有好處，也讓失敗的原因好對應
  const retryAll = async () => {
    setBusy(true);
    let ok = 0;
    let lastErr: unknown;
    for (const f of failed) {
      try {
        await api.post(apiPath("/api/tasks/{id}/retry", { id: f.id }));
        ok++;
        void qc.invalidateQueries({ queryKey: queryKeys.items.detail(f.item_id) });
      } catch (err) {
        lastErr = err;
      }
    }
    setBusy(false);
    void qc.invalidateQueries({ queryKey: queryKeys.queue });
    const done = t("queue.failed.retriedAll", { count: ok, formatted: formatNumber(ok) });
    toast.show({ title: lastErr ? `${done} ${describeError(t, lastErr)}` : done });
  };

  return (
    <section aria-labelledby="failed-title">
      <div className="page__head">
        <h2 id="failed-title" className="section__title">
          {t("queue.failed.title", {
            count: failed.length,
            formatted: formatNumber(failed.length),
          })}
        </h2>
        {failed.length > 1 && (
          <Button onClick={() => void retryAll()} disabled={busy || retry.isPending}>
            {t("queue.failed.retryAll")}
          </Button>
        )}
      </div>
      {failed.length === 0 ? (
        <p className="secondary">{t("queue.failed.none")}</p>
      ) : (
        <ul className="attention" aria-label={t("queue.failed.listLabel")}>
          {failed.map((f) => (
            <FailedRow
              key={f.id}
              f={f}
              disabled={busy || retry.isPending}
              onRetry={() => retry.mutate(f)}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function FailedRow({
  f,
  disabled,
  onRetry,
}: {
  f: FailedTask;
  disabled: boolean;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const detail = useItemDetail(f.item_id);
  const title = detail.data?.title || f.item;
  const badge = badgeOf(f);
  return (
    <li className="trow" data-state={f.status}>
      <Link
        to="/items/$id"
        params={{ id: f.item_id }}
        search={RUNS_TAB}
        className="trow__title"
        lang={detail.data?.original_lang || undefined}
      >
        {title}
      </Link>
      <span>{t(`stage.${f.stage}`, { defaultValue: f.stage })}</span>
      <StatusBadge
        status={badge}
        label={badge === "skipped" ? t("queue.failed.cancelled") : undefined}
      />
      <span className="secondary">{f.message}</span>
      <Button
        variant="text"
        disabled={disabled}
        aria-label={t("queue.failed.retryOne", { item: title })}
        onClick={onRetry}
      >
        {t("common.retry")}
      </Button>
    </li>
  );
}
