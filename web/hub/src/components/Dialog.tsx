// 對話框與抽屜：行為與無障礙交給 Radix Dialog（焦點鎖定、Esc 關閉、aria-modal），外觀用自家 token。
// 抽屜從右側滑出，用於新增、快速檢視與編輯（design-language.md §6.5）。
import * as RadixDialog from "@radix-ui/react-dialog";
import { useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "./Button";

export interface DialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  /** 底部的按鈕列（例如「取消」與主要動作） */
  footer?: ReactNode;
  /** 是否顯示右上角的關閉按鈕；預設顯示 */
  closable?: boolean;
}

function Shell({
  kind,
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  closable = true,
}: DialogProps & { kind: "dialog" | "drawer" }) {
  const { t } = useTranslation();
  // 沒有用 Radix 的 Trigger（開啟的方式很多：按鈕、快捷鍵、貼上網址），自己記住開啟前的焦點，關閉時還回去。
  const returnFocus = useRef<HTMLElement | null>(null);
  return (
    <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="overlay" />
        <RadixDialog.Content
          className={kind}
          onOpenAutoFocus={() => {
            returnFocus.current =
              document.activeElement instanceof HTMLElement ? document.activeElement : null;
          }}
          onCloseAutoFocus={(e) => {
            if (returnFocus.current?.isConnected) {
              e.preventDefault();
              returnFocus.current.focus();
            }
          }}
          // 沒有說明文字時不要讓 Radix 指到不存在的元素
          {...(description ? {} : { "aria-describedby": undefined })}
        >
          <RadixDialog.Title className="dialog__title">{title}</RadixDialog.Title>
          {description && (
            <RadixDialog.Description className="dialog__description">
              {description}
            </RadixDialog.Description>
          )}
          {children}
          {(footer || closable) && (
            <div className="dialog__footer">
              {footer}
              {closable && (
                <RadixDialog.Close asChild>
                  <Button variant="text">{t("common.close")}</Button>
                </RadixDialog.Close>
              )}
            </div>
          )}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}

export function Dialog(props: DialogProps) {
  return <Shell kind="dialog" {...props} />;
}

export function Drawer(props: DialogProps) {
  return <Shell kind="drawer" {...props} />;
}
