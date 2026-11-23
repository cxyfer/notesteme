// /add（ENTRY-R2）：書籤小工具與分享的落地頁。打開「新增」抽屜並帶入網址（照常辨識），
// 不自動開始處理；接著換成媒體庫（replace），重新整理不會再開一次。
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import { useAddDrawer } from "./AddContext";
import { resolveAddEntry } from "./entry";

const route = getRouteApi("/app/add");

export function AddEntryPage() {
  const { t } = useTranslation();
  const search = route.useSearch();
  const navigate = useNavigate();
  const { open } = useAddDrawer();
  const done = useRef(false);

  useEffect(() => {
    if (done.current) return;
    done.current = true;
    const { url, notice } = resolveAddEntry(search);
    void navigate({ to: "/library", replace: true }).then(() => {
      open(url, notice ? t(`add.entry.${notice}`) : undefined);
    });
  }, [search, navigate, open, t]);

  return null;
}
