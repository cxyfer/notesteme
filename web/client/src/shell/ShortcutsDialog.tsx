// 「?」顯示所有快捷鍵（docs/ui/README.md §3.6，UI-7）。只列出已經做好的快捷鍵；lazy 載入。
import { useTranslation } from "react-i18next";

import { Dialog } from "../components";

/** [按鍵, 說明的 key]；按鍵不翻譯 */
const ROWS = [
  ["Ctrl K", "palette"],
  ["N", "add"],
  ["/", "search"],
  ["G L", "library"],
  ["G Q", "queue"],
  ["G S", "subscriptions"],
  ["1–4", "itemTabs"],
  ["?", "help"],
] as const;

export function ShortcutsDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={t("shortcuts.title")}>
      <table className="shortcuts">
        <thead>
          <tr>
            <th scope="col">{t("shortcuts.key")}</th>
            <th scope="col">{t("shortcuts.action")}</th>
          </tr>
        </thead>
        <tbody>
          {ROWS.map(([key, label]) => (
            <tr key={key}>
              <td>
                <kbd>{key}</kbd>
              </td>
              <td>{t(`shortcuts.${label}`)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Dialog>
  );
}
