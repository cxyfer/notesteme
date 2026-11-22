import { forwardRef, type ButtonHTMLAttributes } from "react";

export type ButtonVariant = "primary" | "outline" | "text" | "danger";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** primary：一個畫面最多一個（design-language.md §6.5）；danger：不可復原的操作，執行前先確認 */
  variant?: ButtonVariant;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "outline", className, type = "button", ...rest },
  ref,
) {
  const cls = ["button", `button--${variant}`, className].filter(Boolean).join(" ");
  return <button ref={ref} type={type} className={cls} {...rest} />;
});
