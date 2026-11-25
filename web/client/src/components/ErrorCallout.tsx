// 錯誤說明框（UI 原則 3：出錯時告訴使用者怎麼修）：錯誤類別、訊息、可以直接處理的按鈕。
import { useTranslation } from "react-i18next";

import { describeError } from "../api/describeError";
import { isApiError } from "../api/errors";
import { Button } from "./Button";
import { Icon } from "./Icon";
import { WarningCircle } from "./icons";

/** 錯誤類別（plan 第 5.2 節；後端的 Error 尚未帶類別，見 internal/api/errors.go 的 TODO）。 */
export type ErrorCategory = "retryable" | "wait" | "needs_action" | "fatal";

export interface ErrorAction {
  label: string;
  onClick: () => void;
  primary?: boolean;
}

export interface ErrorCalloutProps {
  error: unknown;
  /** 省略時依狀態碼推測：網路錯誤與 5xx 是 retryable，其餘是 needs_action */
  category?: ErrorCategory;
  /** 覆寫顯示的訊息（例如頁面自己有更具體的說明） */
  message?: string;
  actions?: ErrorAction[];
  /**
   * 預設 true：role="alert"，出現時立刻宣告。放在列表的列裡時設 false
   * （虛擬列表捲動時列會重新掛載，不能每次都宣告；改由頁面層級的 aria-live 宣告狀態變化）。
   */
  live?: boolean;
}

function guessCategory(err: unknown): ErrorCategory {
  if (isApiError(err) && (err.status === 0 || err.status >= 500)) return "retryable";
  return "needs_action";
}

export function ErrorCallout({
  error,
  category,
  message,
  actions = [],
  live = true,
}: ErrorCalloutProps) {
  const { t } = useTranslation();
  const cat = category ?? guessCategory(error);
  return (
    <div className="callout" role={live ? "alert" : undefined} data-category={cat}>
      <Icon icon={WarningCircle} size={16} className="callout__icon" />
      <p className="callout__category">{t(`errors.category.${cat}`)}</p>
      <p className="callout__message">{message ?? describeError(t, error)}</p>
      {actions.length > 0 && (
        <div className="callout__actions">
          {actions.map((a) => (
            <Button key={a.label} variant={a.primary ? "primary" : "outline"} onClick={a.onClick}>
              {a.label}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}
