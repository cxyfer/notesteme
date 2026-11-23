// 「新增」抽屜可以從任何頁面開啟（docs/ui/pages.md §2）：導覽的按鈕、N 快捷鍵、焦點不在輸入框時貼上網址、空狀態的按鈕。
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

import { AddDrawer } from "./AddDrawer";

interface AddApi {
  /** 打開抽屜；url 有值時帶入網址並開始辨識；notice 是抽屜上方的提示（例如書籤帶進來的網址無效，ENTRY-R2） */
  open: (url?: string, notice?: string) => void;
}

const AddContext = createContext<AddApi | undefined>(undefined);

export function useAddDrawer(): AddApi {
  const ctx = useContext(AddContext);
  if (!ctx) throw new Error("useAddDrawer 必須在 <AddProvider> 之內");
  return ctx;
}

export function AddProvider({ children }: { children: ReactNode }) {
  // 每次打開都是新的表單（nonce 當 key），關閉後不保留上次的內容
  const [state, setState] = useState<{
    open: boolean;
    url: string;
    notice?: string;
    nonce: number;
  }>({
    open: false,
    url: "",
    nonce: 0,
  });
  const open = useCallback((url?: string, notice?: string) => {
    setState((s) => ({ open: true, url: url ?? "", notice, nonce: s.nonce + 1 }));
  }, []);
  const value = useMemo(() => ({ open }), [open]);
  return (
    <AddContext.Provider value={value}>
      {children}
      <AddDrawer
        key={state.nonce}
        open={state.open}
        initialUrl={state.url}
        notice={state.notice}
        onOpenChange={(o) => setState((s) => ({ ...s, open: o }))}
      />
    </AddContext.Provider>
  );
}
