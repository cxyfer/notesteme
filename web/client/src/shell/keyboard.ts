// 全域快捷鍵的共用判斷（docs/ui/README.md §3.6）。

/** 焦點在可以輸入文字的元素上時，單鍵快捷鍵（N、/）與「貼上網址」都不作用。 */
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag !== "INPUT") return false;
  const type = (target as HTMLInputElement).type;
  return !["checkbox", "radio", "button", "submit", "reset", "range", "color", "file"].includes(
    type,
  );
}

/** 有對話框或抽屜開著時，不處理全域快捷鍵。 */
export function modalOpen(): boolean {
  return document.querySelector("[role='dialog']") !== null;
}
