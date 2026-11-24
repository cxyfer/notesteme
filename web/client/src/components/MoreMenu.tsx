// 「更多」選單（pages.md §4 的 `⋯`）：disclosure 按鈕（aria-expanded）加一組普通按鈕，
// 不用 role="menu"（那需要完整的方向鍵行為）。Esc 或點選單外面就關閉（Esc 時焦點回到觸發按鈕）。
// 選了項目之後先把焦點還給觸發按鈕再執行動作：動作若打開對話框，關閉時焦點會回到「更多」，
// 不會掉到已卸載的選單項目上。
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "./Button";

export interface MoreMenuItem {
  label: string;
  onSelect: () => void;
  /** 不可復原或會移除東西的動作，用警示色 */
  danger?: boolean;
}

export interface MoreMenuProps {
  items: MoreMenuItem[];
  /** 觸發按鈕的文字；預設是「更多」 */
  label?: string;
}

export function MoreMenu({ items, label }: MoreMenuProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: PointerEvent) {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      e.preventDefault();
      setOpen(false);
      trigger.current?.focus();
    }
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    // 不用 onBlur 關閉：Safari 與 macOS 的 Firefox 點按鈕時不給焦點，blur 的 relatedTarget 是 null，
    // 會在點到選單項目之前就把選單關掉。只靠點外面與 Esc 關閉。
    <div className="more-menu" ref={root}>
      <Button
        ref={trigger}
        aria-expanded={open}
        aria-controls={`${id}-list`}
        onClick={() => setOpen((o) => !o)}
      >
        {label ?? t("common.more")}
      </Button>
      {open && (
        <ul id={`${id}-list`} className="more-menu__list">
          {items.map((it) => (
            <li key={it.label}>
              <Button
                variant={it.danger ? "danger" : "text"}
                onClick={() => {
                  setOpen(false);
                  trigger.current?.focus();
                  it.onSelect();
                }}
              >
                {it.label}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
