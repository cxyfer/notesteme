import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from "react";

export interface TextFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "id"> {
  /** 一定要有可見的標籤（不只用 placeholder） */
  label: ReactNode;
  hint?: ReactNode;
  /** 有值時標成 aria-invalid，並以 aria-describedby 連到訊息 */
  error?: ReactNode;
}

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField(
  { label, hint, error, className, ...rest },
  ref,
) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  return (
    <div className={["field", className].filter(Boolean).join(" ")}>
      <label className="field__label" htmlFor={id}>
        {label}
      </label>
      <input
        ref={ref}
        id={id}
        className="field__input"
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        {...rest}
      />
      {hint && (
        <span id={hintId} className="field__hint">
          {hint}
        </span>
      )}
      {error && (
        <span id={errorId} className="field__error">
          {error}
        </span>
      )}
    </div>
  );
});
