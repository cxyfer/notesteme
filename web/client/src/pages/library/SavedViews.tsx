// 媒體庫的儲存檢視（LIB-R26～R28、pages.md §3 的「存成檢視」）。
//
// 檢視存的是媒體庫的網址參數（filters.ts 的 LibrarySearch，含分組，不含「列｜格」）；
// 套用就是把網址參數整組換成檢視的內容（不是合併），顯示方式維持目前的選擇。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { FILTER_KEYS, validateLibrarySearch, type LibrarySearch } from "./filters";

type ViewQuery = components["schemas"]["ViewQuery"];
type SavedView = components["schemas"]["ViewBody"];

/** 名稱上限（LIB-R27） */
export const MAX_VIEW_NAME = 60;

/** 目前的網址參數 → 檢視的查詢內容：篩選條件與分組，不含顯示方式。 */
export function toViewQuery(s: LibrarySearch): ViewQuery {
  const out: Record<string, unknown> = {};
  for (const k of FILTER_KEYS) if (s[k] !== undefined) out[k] = s[k];
  if (s.group !== undefined) out.group = s.group;
  return out as ViewQuery;
}

/** 檢視的查詢內容 → 網址參數：照媒體庫的規則再驗證一次（舊的檢視可能有現在不認得的值）。 */
export function viewToSearch(q: ViewQuery, view: LibrarySearch["view"]): LibrarySearch {
  return validateLibrarySearch({ ...q, ...(view ? { view } : {}) } as Record<string, unknown>);
}

/** 兩組條件是否相同（標出目前套用中的檢視） */
function sameQuery(a: ViewQuery, b: ViewQuery): boolean {
  const norm = (q: ViewQuery) =>
    JSON.stringify(
      Object.entries(toViewQuery(viewToSearch(q, undefined))).sort(([x], [y]) =>
        x.localeCompare(y),
      ),
    );
  return norm(a) === norm(b);
}

/** 篩選列「已套用」旁的「存成檢視」：輸入名稱的對話框，名稱重複等錯誤顯示在對話框裡。 */
export function SaveViewButton({ search }: { search: LibrarySearch }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();

  function close() {
    setOpen(false);
    setName("");
    setError(undefined);
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (pending || trimmed === "") return;
    setPending(true);
    setError(undefined);
    try {
      await api.post("/api/views", { body: { name: trimmed, query: toViewQuery(search) } });
      await qc.invalidateQueries({ queryKey: queryKeys.views });
      toast.show({ title: t("library.views.saved", { name: trimmed }) });
      close();
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  const formId = "save-view-form";
  return (
    <>
      <Button variant="text" onClick={() => setOpen(true)}>
        {t("library.views.save")}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => (o ? setOpen(true) : close())}
        title={t("library.views.saveTitle")}
        description={t("library.views.saveBody")}
        footer={
          <>
            <Button variant="text" onClick={close}>
              {t("library.views.cancel")}
            </Button>
            <Button
              type="submit"
              form={formId}
              variant="primary"
              disabled={pending || name.trim() === ""}
            >
              {t("library.views.saveConfirm")}
            </Button>
          </>
        }
      >
        <form id={formId} className="settings-form" onSubmit={(e) => void onSubmit(e)} noValidate>
          <TextField
            label={t("library.views.name")}
            hint={t("library.views.nameHint", { max: MAX_VIEW_NAME })}
            value={name}
            maxLength={MAX_VIEW_NAME}
            autoComplete="off"
            required
            onChange={(e) => setName(e.target.value)}
          />
          {error !== undefined && <ErrorCallout error={error} />}
        </form>
      </Dialog>
    </>
  );
}

/** 已存的檢視：點一下套用，每個都可以刪除（先確認）。沒有檢視時不顯示。 */
export function SavedViewsList({
  search,
  onApply,
}: {
  search: LibrarySearch;
  onApply: (next: LibrarySearch) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const query = useQuery({ queryKey: queryKeys.views, queryFn: () => api.get("/api/views") });
  const [deleting, setDeleting] = useState<SavedView>();
  const [error, setError] = useState<unknown>();

  async function remove(v: SavedView) {
    setDeleting(undefined);
    setError(undefined);
    try {
      await api.delete<"/api/views/{id}">(apiPath("/api/views/{id}", { id: v.id }));
      await qc.invalidateQueries({ queryKey: queryKeys.views });
      toast.show({ title: t("library.views.deleted", { name: v.name }) });
    } catch (err) {
      setError(err);
    }
  }

  if (query.isError) {
    return (
      <ErrorCallout
        error={query.error}
        message={t("library.views.loadError")}
        actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
      />
    );
  }
  const views = query.data?.views ?? [];
  if (views.length === 0) return null;
  const current = toViewQuery(search);
  return (
    <section className="saved-views" aria-labelledby="saved-views-title">
      <h2 id="saved-views-title" className="saved-views__title">
        {t("library.views.title")}
      </h2>
      <ul className="chips">
        {views.map((v) => {
          const active = sameQuery(v.query, current);
          return (
            <li className="chip saved-view" key={v.id} data-active={active || undefined}>
              <button
                type="button"
                className="saved-view__apply"
                aria-pressed={active}
                onClick={() => onApply(viewToSearch(v.query, search.view))}
              >
                {v.name}
              </button>
              <button
                type="button"
                className="chip__remove"
                aria-label={t("library.views.delete", { name: v.name })}
                onClick={() => setDeleting(v)}
              >
                <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                  <path
                    d="m4.5 4.5 7 7m0-7-7 7"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                  />
                </svg>
              </button>
            </li>
          );
        })}
      </ul>
      {error !== undefined && <ErrorCallout error={error} />}
      <Dialog
        open={deleting !== undefined}
        onOpenChange={(o) => !o && setDeleting(undefined)}
        title={t("library.views.deleteTitle", { name: deleting?.name ?? "" })}
        description={t("library.views.deleteBody")}
        closable={false}
        footer={
          <>
            <Button variant="text" onClick={() => setDeleting(undefined)}>
              {t("library.views.cancel")}
            </Button>
            <Button variant="danger" onClick={() => deleting && void remove(deleting)}>
              {t("library.views.deleteConfirm")}
            </Button>
          </>
        }
      />
    </section>
  );
}
