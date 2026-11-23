// 垃圾桶（pages.md §3「垃圾桶」、DEL-R4、R12）：刪除時間、幾天後永久刪除、還原、永久刪除、清空。lazy route。
// 還原可以再刪除，不跳確認框；永久刪除與清空無法復原，先確認一次（README §3.5）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, EmptyState, ErrorCallout, Spinner, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import "../../styles/ui7.css";

type Trashed = components["schemas"]["TrashedItem"];

/** 距離永久刪除還有幾天（無條件進位，最少 0） */
export function daysLeft(purgeAt: string, now = Date.now()) {
  return Math.max(0, Math.ceil((new Date(purgeAt).getTime() - now) / 86_400_000));
}

export function TrashPage() {
  const { t } = useTranslation();
  usePageTitle(t("trash.title"));
  const qc = useQueryClient();
  const toast = useToast();
  const list = useQuery({
    queryKey: queryKeys.trash,
    queryFn: ({ signal }) => api.get("/api/trash", { signal }),
  });
  // 要確認的動作：永久刪除一部或清空
  const [confirm, setConfirm] = useState<{ item?: Trashed } | null>(null);
  const changed = () => {
    void qc.invalidateQueries({ queryKey: queryKeys.trash });
    void qc.invalidateQueries({ queryKey: queryKeys.items.lists() });
    void qc.invalidateQueries({ queryKey: queryKeys.overviewAll });
  };
  const restore = useMutation({
    mutationFn: (id: string) => api.post(apiPath("/api/trash/{id}/restore", { id })),
    onSuccess: () => {
      changed();
      toast.show({ title: t("item.delete.restored") });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const purge = useMutation({
    mutationFn: (item?: Trashed) =>
      item ? api.delete(apiPath("/api/trash/{id}", { id: item.id })) : api.delete("/api/trash"),
    onSuccess: (_, item) => {
      changed();
      setConfirm(null);
      toast.show({ title: t(item ? "trash.purged" : "trash.emptied") });
    },
  });
  const items = list.data?.items ?? [];

  return (
    <div className="trash">
      <nav className="item__crumbs" aria-label={t("item.breadcrumb")}>
        <Link to="/library">{t("pages.library.title")}</Link>
      </nav>
      <div className="page__head">
        <h1 className="page__title">{t("trash.title")}</h1>
        {items.length > 0 && (
          <Button variant="danger" onClick={() => setConfirm({})}>
            {t("trash.empty")}
          </Button>
        )}
      </div>
      {list.isPending ? (
        <Spinner label={t("common.loading")} showLabel />
      ) : list.isError ? (
        <ErrorCallout
          error={list.error}
          actions={[{ label: t("common.retry"), onClick: () => void list.refetch() }]}
        />
      ) : items.length === 0 ? (
        <EmptyState message={t("trash.none")} />
      ) : (
        <ul className="ov-list trash__list">
          {items.map((it) => (
            <li key={it.id} className="trash__row">
              <div>
                <p className="trash__title">{it.title || it.ref}</p>
                <p className="secondary num">
                  {t("trash.deletedAt", { at: formatDateTime(it.deleted_at) })}
                  {t("common.listSeparator")}
                  {t("trash.purgeIn", { count: daysLeft(it.purge_at) })}
                </p>
              </div>
              <div className="trash__actions">
                <Button disabled={restore.isPending} onClick={() => restore.mutate(it.id)}>
                  {t("trash.restore")}
                </Button>
                <Button variant="text" onClick={() => setConfirm({ item: it })}>
                  {t("trash.purge")}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <Dialog
        open={confirm !== null}
        onOpenChange={(o) => !o && !purge.isPending && setConfirm(null)}
        title={
          confirm?.item
            ? t("trash.purgeTitle", { title: confirm.item.title || confirm.item.ref })
            : t("trash.emptyTitle", { count: items.length })
        }
        description={t("trash.purgeBody")}
        footer={
          <Button
            variant="danger"
            disabled={purge.isPending}
            onClick={() => purge.mutate(confirm?.item)}
          >
            {confirm?.item ? t("trash.purge") : t("trash.empty")}
          </Button>
        }
      >
        {purge.isError && <ErrorCallout error={purge.error} />}
      </Dialog>
    </div>
  );
}
