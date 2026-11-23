// 媒體庫「等你決定」檢視的批次繼續（DIG-R7）：把已載入的影音送 POST /api/items/continue（每批最多 100 部）。
// 列表的條件是近似的（見 filters.ts 的 blocked），不是等你決定的影音由伺服器略過（not_awaiting_user），不算錯誤。
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { Button, Dialog, ErrorCallout, useToast } from "../../components";

/** API 的上限：一次 1～100 部 */
const BATCH = 100;

export function ContinueBar({ ids, onDone }: { ids: readonly string[]; onDone: () => void }) {
  const { t } = useTranslation();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();

  async function run() {
    setPending(true);
    setError(undefined);
    let continued = 0;
    let skipped = 0;
    try {
      for (let i = 0; i < ids.length; i += BATCH) {
        const out = await api.post("/api/items/continue", {
          body: { ids: ids.slice(i, i + BATCH) },
        });
        continued += out?.continued?.length ?? 0;
        skipped += out?.skipped?.length ?? 0;
      }
      setOpen(false);
      toast.show({ title: t("library.awaiting.done", { continued, skipped }) });
      onDone();
    } catch (err) {
      // 前面的批次已經繼續了：仍重新載入，並顯示錯誤
      setError(err);
      if (continued > 0) {
        toast.show({ title: describeError(t, err) });
        onDone();
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="library__awaiting" role="group" aria-label={t("library.awaiting.chip")}>
      <p className="secondary">{t("library.awaiting.hint")}</p>
      <Button variant="primary" onClick={() => setOpen(true)}>
        {t("library.awaiting.continueAll", { count: ids.length })}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => !pending && setOpen(o)}
        title={t("library.awaiting.confirmTitle", { count: ids.length })}
        description={t("library.awaiting.confirmBody")}
        footer={
          <Button variant="primary" disabled={pending} onClick={() => void run()}>
            {t("library.awaiting.confirm")}
          </Button>
        }
      >
        {error !== undefined && <ErrorCallout error={error} />}
      </Dialog>
    </div>
  );
}
