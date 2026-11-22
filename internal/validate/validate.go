// Package validate 放跨套件共用的驗證結果型別。
// pipeline（處理方案與設定驗證）、prompts（prompt 庫）與 api（422 回應的 errors）都用同一個 FieldError，
// 才不會讓它們互相依賴。
package validate

// FieldError 是一個欄位的錯誤。Path 以點號分隔，陣列索引用數字（例如 outputs.0.target）。
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Prefix 回傳加上路徑前綴的複本，不修改輸入（SEC-R10 的 config import 會用到，
// 例如 profiles.broken.language.targets）。prefix 為空時只複製；Path 為空時路徑就是 prefix 本身。
func Prefix(prefix string, errs []FieldError) []FieldError {
	if errs == nil {
		return nil
	}
	out := make([]FieldError, len(errs))
	for i, e := range errs {
		switch {
		case prefix == "":
		case e.Path == "":
			e.Path = prefix
		default:
			e.Path = prefix + "." + e.Path
		}
		out[i] = e
	}
	return out
}
