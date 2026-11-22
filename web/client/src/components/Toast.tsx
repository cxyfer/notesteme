// Toast：左下、Ink 底、5 秒後消失，可以有一個動作按鈕（例如「復原」，design-language.md §6.5、README §3.5）。
// 用法：const toast = useToast(); toast.show({ title: t("..."), action: { label: t("common.undo"), onClick } })。
import * as RadixToast from "@radix-ui/react-toast";
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export interface ToastOptions {
  title: string;
  action?: { label: string; onClick: () => void };
  /** 毫秒；預設 5000 */
  duration?: number;
}

interface ToastItem extends ToastOptions {
  id: number;
}

interface ToastApi {
  show: (opts: ToastOptions) => void;
}

const ToastContext = createContext<ToastApi | undefined>(undefined);

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast 必須在 <ToastProvider> 之內");
  return ctx;
}

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [items, setItems] = useState<ToastItem[]>([]);
  const show = useCallback((opts: ToastOptions) => {
    setItems((xs) => [...xs, { ...opts, id: nextId++ }]);
  }, []);
  const remove = (id: number) => setItems((xs) => xs.filter((x) => x.id !== id));
  const value = useMemo(() => ({ show }), [show]);
  return (
    <ToastContext.Provider value={value}>
      <RadixToast.Provider duration={5000} swipeDirection="left" label={t("common.notifications")}>
        {children}
        {items.map((it) => (
          <RadixToast.Root
            key={it.id}
            className="toast"
            duration={it.duration}
            onOpenChange={(open) => {
              if (!open) remove(it.id);
            }}
          >
            <RadixToast.Title className="toast__title">{it.title}</RadixToast.Title>
            {it.action && (
              <RadixToast.Action asChild altText={it.action.label}>
                <button type="button" className="button button--text" onClick={it.action.onClick}>
                  {it.action.label}
                </button>
              </RadixToast.Action>
            )}
          </RadixToast.Root>
        ))}
        <RadixToast.Viewport className="toast-viewport" />
      </RadixToast.Provider>
    </ToastContext.Provider>
  );
}
