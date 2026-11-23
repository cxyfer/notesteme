// 我的分享（GET /api/v1/me/shares，upload）：每次分享的版本、狀態與被移除的原因；可以撤回（HREP-R10）。
// 撤回只在 Hub 網頁進行（Client 沒有撤回的路由，HSHR S12）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { MyShareView } from "../../api/types";
import {
  Button,
  ConfirmDialog,
  EmptyState,
  ErrorCallout,
  Spinner,
  useToast,
} from "../../components";
import { formatDateTime } from "../../i18n";
import { kindLabel, videoLabel, videoLink } from "../versionText";

export function MyShares() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [withdrawing, setWithdrawing] = useState<MyShareView | null>(null);
  const q = useQuery({ queryKey: queryKeys.shares, queryFn: () => api.get("/api/v1/me/shares") });
  const withdraw = useMutation({
    mutationFn: (id: string) =>
      api.delete<"/api/v1/shares/{id}">(apiPath("/api/v1/shares/{id}", { id })),
    onSuccess: () => {
      setWithdrawing(null);
      toast.show({ title: t("shares.withdrawn") });
      void qc.invalidateQueries({ queryKey: queryKeys.shares });
      void qc.invalidateQueries({ queryKey: ["versions"] });
    },
  });
  const shares = q.data?.shares ?? [];
  return (
    <section aria-labelledby="shares-title" className="panel">
      <h2 id="shares-title">{t("shares.title")}</h2>
      {q.isPending ? (
        <Spinner showLabel />
      ) : q.isError ? (
        <ErrorCallout error={q.error} />
      ) : shares.length === 0 ? (
        <EmptyState message={t("shares.empty")} />
      ) : (
        <ul className="rows">
          {shares.map((s) => (
            <li key={s.id} className="share">
              <div className="share__head">
                <Link {...videoLink(s.video)} className="mono">
                  {videoLabel(s.video)}
                </Link>
                <span className="secondary">{formatDateTime(s.created_at)}</span>
                {s.withdrawn_at ? (
                  <span className="badge">
                    {t("shares.withdrawnAt", { at: formatDateTime(s.withdrawn_at) })}
                  </span>
                ) : (
                  <Button
                    variant="text"
                    onClick={() => {
                      withdraw.reset();
                      setWithdrawing(s);
                    }}
                  >
                    {t("shares.withdraw")}
                  </Button>
                )}
              </div>
              <ul className="share__versions">
                {(s.versions ?? []).map((v) => (
                  <li key={v.id}>
                    <Link to="/versions/$id" params={{ id: v.id }}>
                      {kindLabel(t, v.kind, v.variant, v.lang)}
                    </Link>{" "}
                    <span className="badge">
                      {t(`state.${v.state}`, { defaultValue: v.state })}
                    </span>
                    {v.removal && (
                      <span className="secondary">
                        {" "}
                        {t("shares.removal", {
                          source: t(`removalSource.${v.removal.source}`, {
                            defaultValue: v.removal.source,
                          }),
                          reason: v.removal.reason,
                        })}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={withdrawing !== null}
        onOpenChange={(o) => !o && setWithdrawing(null)}
        title={t("shares.withdrawTitle")}
        description={t("shares.withdrawHint")}
        confirmLabel={t("shares.withdraw")}
        danger
        pending={withdraw.isPending}
        error={withdraw.error}
        onConfirm={() => withdrawing && withdraw.mutate(withdrawing.id)}
      />
    </section>
  );
}
