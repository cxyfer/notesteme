import { forwardRef, useId, type ReactNode, type SelectHTMLAttributes } from "react";

export interface SelectFieldProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, "id"> {
  /** 一定要有可見的標籤 */
  label: ReactNode;
  options: readonly { value: string; label: string }[];
  hint?: ReactNode;
  /** 有值時標成 aria-invalid，並以 aria-describedby 連到訊息 */
  error?: ReactNode;
}

/** 下拉選單（與 TextField 同一套外觀與標籤、說明、錯誤的接法）。 */
export const SelectField = forwardRef<HTMLSelectElement, SelectFieldProps>(function SelectField(
  { label, options, hint, error, className, ...rest },
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
      <select
        ref={ref}
        id={id}
        className="field__input"
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        {...rest}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
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
