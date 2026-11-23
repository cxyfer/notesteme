// 選一部影音（Prompt 試跑、字幕樣式預覽都要指定影音）：先用標題搜尋，再從結果中選。
// 只列最近的 50 部（GET /api/items 的第一頁）；找不到時輸入標題關鍵字縮小範圍。
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../api/client";
import { queryKeys } from "../api/queryKeys";
import { SelectField } from "./SelectField";
import { TextField } from "./TextField";

export interface ItemPickerProps {
  value: string;
  onChange: (id: string) => void;
}

const PICKER_LIMIT = 50;

export function ItemPicker({ value, onChange }: ItemPickerProps) {
  const { t } = useTranslation();
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  // 打字中延遲 300ms 再查詢
  useEffect(() => {
    const h = setTimeout(() => setQ(text.trim()), 300);
    return () => clearTimeout(h);
  }, [text]);
  const items = useQuery({
    queryKey: queryKeys.items.picker(q),
    queryFn: ({ signal }) =>
      api.get("/api/items", { query: { limit: PICKER_LIMIT, q: q || undefined }, signal }),
    staleTime: 60_000,
  });
  const list = items.data?.items ?? [];
  const options = [
    { value: "", label: items.isPending ? t("common.loading") : t("itemPicker.choose") },
    ...list.map((it) => ({ value: it.id, label: it.title || it.ref })),
  ];
  // 目前選的影音不在結果中（例如換了搜尋字）時仍保留選項
  if (value && !list.some((it) => it.id === value)) {
    options.push({ value, label: value });
  }
  return (
    <div className="item-picker">
      <TextField
        label={t("itemPicker.search")}
        type="search"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      <SelectField
        label={t("itemPicker.item")}
        value={value}
        options={options}
        onChange={(e) => onChange(e.target.value)}
        hint={!items.isPending && list.length === 0 ? t("itemPicker.none") : undefined}
      />
    </div>
  );
}
