// 詳情頁的小型對話框：取消處理（PIPE-R15）、上傳 cookie（SEC-R4；上傳後等待它的步驟自動恢復）。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, useToast } from "../../components";

export function CancelDialog({
  open,
  onOpenChange,
  itemId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  itemId: string;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const cancel = useMutation({
    mutationFn: () =>
      api.post<"/api/items/{id}/cancel">(apiPath("/api/items/{id}/cancel", { id: itemId })),
    onSuccess: (d) => {
      if (d) qc.setQueryData(queryKeys.items.detail(itemId), d);
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemId) });
      void qc.invalidateQueries({ queryKey: queryKeys.queue });
      toast.show({ title: t("item.cancel.done") });
      onOpenChange(false);
    },
  });
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("item.cancel.title")}
      description={t("item.cancel.description")}
      footer={
        <Button variant="danger" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
          {t("item.cancel.confirm")}
        </Button>
      }
    >
      {cancel.isError && <ErrorCallout error={cancel.error} />}
    </Dialog>
  );
}

export function CookieDialog({
  open,
  onOpenChange,
  platform,
  itemId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  platform: string;
  itemId: string;
}) {
  const { t } = useTranslation();
  const id = useId();
  const qc = useQueryClient();
  const toast = useToast();
  const [content, setContent] = useState("");
  const supported = platform === "youtube" || platform === "bilibili";
  const upload = useMutation({
    mutationFn: () => api.post("/api/cookies", { body: { platform, content } }),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemId) });
      toast.show({ title: t("item.cookie.done", { count: r?.cookies ?? 0 }) });
      setContent("");
      onOpenChange(false);
    },
  });
  async function readFile(f: File | undefined) {
    if (!f) return;
    // 上限 64 KiB（API 的限制）；更大的檔案不讀進記憶體
    if (f.size > 64 * 1024) {
      toast.show({ title: t("item.cookie.tooLarge") });
      return;
    }
    setContent(await f.text());
  }
  // cookie 是敏感內容：關閉對話框時一律清掉
  const close = (o: boolean) => {
    if (!o) {
      setContent("");
      upload.reset();
    }
    onOpenChange(o);
  };
  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title={t("item.cookie.title", {
        platform: t(`platform.${platform}`, { defaultValue: platform }),
      })}
      description={t("item.cookie.description")}
      footer={
        <Button
          variant="primary"
          disabled={!supported || !content.trim() || upload.isPending}
          onClick={() => upload.mutate()}
        >
          {t("item.cookie.submit")}
        </Button>
      }
    >
      {!supported ? (
        <p>{t("item.cookie.unsupported")}</p>
      ) : (
        <div className="form">
          <div className="field">
            <label className="field__label" htmlFor={`${id}-file`}>
              {t("item.cookie.file")}
            </label>
            <input
              id={`${id}-file`}
              type="file"
              accept=".txt,text/plain"
              onChange={(e) => void readFile(e.target.files?.[0])}
            />
          </div>
          <div className="field">
            <label className="field__label" htmlFor={`${id}-text`}>
              {t("item.cookie.paste")}
            </label>
            <textarea
              id={`${id}-text`}
              className="field__input field__textarea"
              rows={5}
              value={content}
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => setContent(e.target.value)}
            />
          </div>
        </div>
      )}
      {upload.isError && <ErrorCallout error={upload.error} />}
    </Dialog>
  );
}
