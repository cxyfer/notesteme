// 影音詳情的「標為已讀／未讀」（LIB-R19，UI-7）：打開詳情不會自動標為已讀，用批次 API 送單一影音。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import { Button, useToast } from "../../components";

export function ReadStateButton({ id, state }: { id: string; state: "read" | "unread" }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const action = state === "unread" ? "mark_read" : "mark_unread";
  const m = useMutation({
    mutationFn: () => api.post("/api/items/batch", { body: { action, ids: [id] } }),
    onSuccess: (out) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(id) });
      // 單部的錯誤在 items[0]（LIB-R15），HTTP 仍是 200
      const it = out?.items?.[0];
      toast.show({
        title:
          it && !it.ok
            ? (it.message ?? it.code ?? "")
            : t(`library.bulk.${action}Done`, { count: 1 }),
      });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  return (
    <Button disabled={m.isPending} onClick={() => m.mutate()}>
      {t(`library.bulk.${action}`)}
    </Button>
  );
}
