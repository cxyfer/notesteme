// 確認框：不可復原的操作（撤回分享、撤銷 token、下架決定、停用帳號）先確認（docs/ui/README.md §3.5）。
// children 可以放額外的欄位（例如說明、原因）；pending 時按鈕停用，錯誤顯示在框內。
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "./Button";
import { Dialog } from "./Dialog";
import { ErrorCallout } from "./ErrorCallout";

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  pending?: boolean;
  error?: unknown;
  onConfirm: () => void;
  children?: ReactNode;
}

export function ConfirmDialog(p: ConfirmDialogProps) {
  const { t } = useTranslation();
  return (
    <Dialog
      open={p.open}
      onOpenChange={p.onOpenChange}
      title={p.title}
      description={p.description}
      closable={false}
      footer={
        <>
          <Button variant="text" onClick={() => p.onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            variant={p.danger ? "danger" : "primary"}
            disabled={p.pending}
            onClick={p.onConfirm}
          >
            {p.confirmLabel}
          </Button>
        </>
      }
    >
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault();
          p.onConfirm();
        }}
      >
        {p.children}
        {p.error ? <ErrorCallout error={p.error} /> : null}
      </form>
    </Dialog>
  );
}
