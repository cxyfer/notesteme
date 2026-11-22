// 刪除到垃圾桶（DEL-R1、R3、R9；pages.md §4「更多」選單）：確認對話框列出會失去的產出數、可釋放的空間、
// 已花費的金額（刪除不會退費）。有輸出檔時多一個選項「同時刪除未修改過的輸出檔」，預設不勾；
// 改過的檔案列出路徑並標示「已修改，不會刪除」。還原時被刪除的輸出檔會由 publish 重新寫回（DEL-R9）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, ErrorCallout, Spinner, useToast } from "../../components";
import { formatBytes, formatNumber, formatUsd } from "../../i18n";

type Trashed = components["schemas"]["TrashItemOutputBody"];

export function DeleteDialog({
  open,
  onOpenChange,
  itemId,
  title,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  itemId: string;
  title: string;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [deleteOutputs, setDeleteOutputs] = useState(false);
  const preview = useQuery({
    queryKey: queryKeys.items.deletionPreview(itemId),
    queryFn: ({ signal }) =>
      api.get<"/api/items/{id}/deletion-preview">(
        apiPath("/api/items/{id}/deletion-preview", { id: itemId }),
        { signal },
      ),
    enabled: open,
    staleTime: 0,
  });
  const p = preview.data;
  const removable = (p?.outputs ?? []).filter((o) => !o.modified);
  const kept = (p?.outputs ?? []).filter((o) => o.modified);

  const restore = useMutation({
    mutationFn: () => api.post(apiPath("/api/trash/{id}/restore", { id: itemId })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.lists() });
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemId) });
      void qc.invalidateQueries({ queryKey: queryKeys.trash });
      toast.show({ title: t("item.delete.restored") });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });

  const del = useMutation({
    mutationFn: () =>
      api.delete<"/api/items/{id}">(apiPath("/api/items/{id}", { id: itemId }), {
        body: deleteOutputs ? { delete_outputs: true } : {},
      }) as Promise<Trashed>,
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.lists() });
      void qc.invalidateQueries({ queryKey: queryKeys.trash });
      qc.removeQueries({ queryKey: queryKeys.items.detail(itemId) });
      const keptN = r?.kept_outputs?.length ?? 0;
      toast.show({
        title: keptN
          ? t("item.delete.doneKept", { count: keptN, formatted: formatNumber(keptN) })
          : t("item.delete.done"),
        action: { label: t("common.undo"), onClick: () => restore.mutate() },
        duration: 10_000,
      });
      onOpenChange(false);
      void navigate({ to: "/library" });
    },
  });

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("item.delete.title")}
      description={t("item.delete.description", { title })}
      footer={
        <Button
          variant="danger"
          onClick={() => del.mutate()}
          disabled={del.isPending || preview.isPending}
        >
          {del.isPending ? t("item.delete.deleting") : t("item.delete.confirm")}
        </Button>
      }
    >
      {preview.isPending ? (
        <Spinner label={t("item.delete.loading")} showLabel />
      ) : preview.isError ? (
        <ErrorCallout
          error={preview.error}
          actions={[{ label: t("common.retry"), onClick: () => void preview.refetch() }]}
        />
      ) : (
        p && (
          <div className="delete">
            <ul className="delete__facts">
              <li>
                {t("item.delete.artifacts", {
                  count: p.artifacts,
                  formatted: formatNumber(p.artifacts),
                })}
              </li>
              <li>{t("item.delete.bytes", { size: formatBytes(p.bytes) })}</li>
              <li>{t("item.delete.spent", { amount: formatUsd(p.spent_usd) })}</li>
            </ul>
            <p className="secondary">{t("item.delete.restoreHint")}</p>
            {removable.length > 0 && (
              <label className="check">
                <input
                  type="checkbox"
                  checked={deleteOutputs}
                  onChange={(e) => setDeleteOutputs(e.target.checked)}
                />
                <span>
                  {t("item.delete.deleteOutputs", {
                    count: removable.length,
                    formatted: formatNumber(removable.length),
                  })}
                </span>
              </label>
            )}
            {removable.length > 0 && deleteOutputs && (
              <p className="secondary">{t("item.delete.deleteOutputsHint")}</p>
            )}
            {kept.length > 0 && (
              <div>
                <p>{t("item.delete.keptTitle")}</p>
                <ul className="delete__kept">
                  {kept.map((o) => (
                    <li key={o.path}>
                      <code className="path">{o.path}</code>{" "}
                      <span className="secondary">
                        {t(`item.delete.keptReason.${o.reason || "modified"}`, {
                          defaultValue: t("item.delete.keptReason.modified"),
                        })}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )
      )}
      {del.isError && <ErrorCallout error={del.error} />}
    </Dialog>
  );
}
