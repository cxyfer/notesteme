// 圖示（design-language.md §5.4）：Phosphor Icons 的 Regular 線條，單獨出現 20px、與文字一起出現 16px。
// 圖示不單獨表達意思（旁邊一定有文字，或按鈕有 aria-label）：一律 aria-hidden；顏色是 Phosphor 預設的 currentColor（繼承文字色）。
// 圖示要從 `@phosphor-icons/react/dist/csr/<名稱>` 逐一匯入：只有用到的圖示會進 bundle（初始 JS 的預算見 NFR-PERF-10）。
import type { Icon as PhosphorIcon } from "@phosphor-icons/react";

export type IconComponent = PhosphorIcon;

export interface IconProps {
  icon: PhosphorIcon;
  /** 20：單獨出現；16：與文字一起出現 */
  size?: 16 | 20;
  /** 只有 §6.4 指定實心的圖示（「完成」的實心勾）用 fill；打包時只保留 regular 與這些 fill（scripts/phosphor-regular.ts） */
  weight?: "regular" | "fill";
  className?: string;
}

export function Icon({ icon: Glyph, size = 20, weight = "regular", className }: IconProps) {
  return (
    <Glyph
      size={size}
      weight={weight}
      aria-hidden
      focusable={false}
      className={className ? `icon ${className}` : "icon"}
    />
  );
}
