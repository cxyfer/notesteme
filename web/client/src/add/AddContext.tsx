// 「新增」抽屜可以從任何頁面開啟（docs/ui/pages.md §2）：導覽的按鈕、N 快捷鍵、焦點不在輸入框時貼上網址、空狀態的按鈕、
// 把檔案拖進視窗（document 層的 drop，帶入檔案；上傳 API 一次只收一個檔案，多個時帶入第一個並提示）。
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";

import { AddDrawer } from "./AddDrawer";

interface AddApi {
  /**
   * 打開抽屜；url 有值時帶入網址並開始辨識；notice 是抽屜上方的提示（例如書籤帶進來的網址無效，ENTRY-R2）；
   * file 有值時帶入要上傳的檔案（把檔案拖進視窗）
   */
  open: (url?: string, notice?: string, file?: File) => void;
}

const AddContext = createContext<AddApi | undefined>(undefined);

export function useAddDrawer(): AddApi {
  const ctx = useContext(AddContext);
  if (!ctx) throw new Error("useAddDrawer 必須在 <AddProvider> 之內");
  return ctx;
}

export function AddProvider({ children }: { children: ReactNode }) {
  // 每次打開都是新的表單（nonce 當 key），關閉後不保留上次的內容
  const { t } = useTranslation();
  const [state, setState] = useState<{
    open: boolean;
    url: string;
    notice?: string;
    file?: File;
    nonce: number;
  }>({
    open: false,
    url: "",
    nonce: 0,
  });
  const open = useCallback((url?: string, notice?: string, file?: File) => {
    setState((s) => ({ open: true, url: url ?? "", notice, file, nonce: s.nonce + 1 }));
  }, []);
  const value = useMemo(() => ({ open }), [open]);

  // 把檔案拖進視窗（pages.md §2、ENTRY 的「檔案請用『新增』的拖放」）
  useEffect(() => {
    const hasFiles = (e: DragEvent) =>
      !!e.dataTransfer && [...e.dataTransfer.types].includes("Files");
    const ignore = (e: DragEvent) =>
      e.defaultPrevented ||
      !hasFiles(e) ||
      (e.target instanceof Element && e.target.closest("input[type='file']") !== null);
    // 只有拖的是檔案時才接手：文字、連結的拖放（例如拖進輸入框）照瀏覽器原本的行為
    function onDragOver(e: DragEvent) {
      if (ignore(e)) return;
      e.preventDefault();
      e.dataTransfer!.dropEffect = "copy";
    }
    function onDrop(e: DragEvent) {
      if (ignore(e)) return;
      // 不讓瀏覽器直接打開檔案
      e.preventDefault();
      const files = [...(e.dataTransfer?.files ?? [])];
      const first = files[0];
      if (!first) return;
      open(
        undefined,
        files.length > 1
          ? t("add.dropOnlyFirst", { total: files.length, name: first.name })
          : undefined,
        first,
      );
    }
    document.addEventListener("dragover", onDragOver);
    document.addEventListener("drop", onDrop);
    return () => {
      document.removeEventListener("dragover", onDragOver);
      document.removeEventListener("drop", onDrop);
    };
  }, [open, t]);
  return (
    <AddContext.Provider value={value}>
      {children}
      <AddDrawer
        key={state.nonce}
        open={state.open}
        initialUrl={state.url}
        initialFile={state.file}
        notice={state.notice}
        onOpenChange={(o) => setState((s) => ({ ...s, open: o }))}
      />
    </AddContext.Provider>
  );
}
