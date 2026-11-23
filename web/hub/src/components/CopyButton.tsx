// 複製文字（Hub token、版本 ID）。剪貼簿不可用時提示使用者自己選取。
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "./Button";

export function CopyButton({ text, label }: { text: string; label?: string }) {
  const { t } = useTranslation();
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  return (
    <span className="copy">
      <Button
        variant="outline"
        onClick={() => {
          if (!navigator.clipboard) {
            setState("failed");
            return;
          }
          navigator.clipboard.writeText(text).then(
            () => setState("copied"),
            () => setState("failed"),
          );
        }}
      >
        {label ?? t("common.copy")}
      </Button>
      <span className="copy__status" role="status">
        {state === "copied" ? t("common.copied") : state === "failed" ? t("common.copyFailed") : ""}
      </span>
    </span>
  );
}
