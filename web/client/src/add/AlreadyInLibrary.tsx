// 「新增」抽屜的「已在媒體庫中」（pages.md §2：已經處理過的影音標示出來，並提供「打開」或「重新處理」）。
//
//   - 辨識後就標示：POST /api/estimate 的 items[].already_in_library（不含垃圾桶中的影音）。
//     估價只有 ref，沒有影音 ID；GET /api/items/{id} 接受 ref，rerun 只接受 ID，所以按下按鈕時才用 ref 查一次。
//   - 送出之後才知道的（估價失敗、或 POST 回 already_in_library）沿用 POST 回傳的 ID。
//   - 「重新處理」打開影音詳情的同一個重新處理對話框（RerunDialog，lazy 載入），照它的選項與確認。
import { useMutation } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../api/client";
import { Button, ErrorCallout } from "../components";

const RerunDialog = lazy(() =>
  import("../pages/item/RerunDialog").then((m) => ({ default: m.RerunDialog })),
);

export interface LibraryEntry {
  /** 影音 ID（送出之後才知道）或 ref（估價） */
  id?: string;
  ref?: string;
  title: string;
  lang?: string;
  inTrash?: boolean;
}

export function AlreadyInLibrary({
  entries,
  message,
  onOpen,
}: {
  entries: readonly LibraryEntry[];
  message: string;
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  // 對話框關閉時不卸載（只把 open 設成 false），Radix 才會把外層抽屜的 aria-hidden 與 pointer-events 還原
  const [rerun, setRerun] = useState<{ id: string; stages: string[]; open: boolean } | null>(null);
  const lookup = useMutation({
    mutationFn: (idOrRef: string) =>
      api.get<"/api/items/{id}">(apiPath("/api/items/{id}", { id: idOrRef })),
  });

  async function detailOf(e: LibraryEntry) {
    try {
      return await lookup.mutateAsync((e.id ?? e.ref)!);
    } catch {
      return undefined; // 錯誤由下面的 ErrorCallout 顯示
    }
  }

  async function open(e: LibraryEntry) {
    if (e.id) return onOpen(e.id);
    const d = await detailOf(e);
    if (d) onOpen(d.id);
  }

  async function startRerun(e: LibraryEntry) {
    const d = await detailOf(e);
    if (d) setRerun({ id: d.id, stages: (d.stages ?? []).map((s) => s.stage), open: true });
  }

  return (
    <div className="add__existing" role="status">
      <p>{message}</p>
      <ul>
        {entries.map((e) => (
          <li key={e.id ?? e.ref}>
            <span lang={e.lang || undefined}>{e.title || e.ref}</span>
            <span className="add__existing-actions">
              <Button
                variant="text"
                disabled={lookup.isPending}
                aria-label={
                  e.inTrash ? undefined : t("add.openNamed", { title: e.title || e.ref || "" })
                }
                onClick={() => void open(e)}
              >
                {e.inTrash ? t("add.inTrash") : t("add.openItem")}
              </Button>
              {!e.inTrash && (
                <Button
                  variant="text"
                  disabled={lookup.isPending}
                  aria-label={t("add.rerunNamed", { title: e.title || e.ref || "" })}
                  onClick={() => void startRerun(e)}
                >
                  {t("add.rerun")}
                </Button>
              )}
            </span>
          </li>
        ))}
      </ul>
      {lookup.isError && <ErrorCallout error={lookup.error} />}
      {rerun && (
        <Suspense fallback={null}>
          <RerunDialog
            open={rerun.open}
            onOpenChange={(o) => setRerun((r) => (r ? { ...r, open: o } : r))}
            itemId={rerun.id}
            stages={rerun.stages}
          />
        </Suspense>
      )}
    </div>
  );
}
