package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// Huma 自己產生的錯誤也是這個格式，而且用繁中的預設訊息。
func TestHumaNewErrorUsesSharedFormat(t *testing.T) {
	err := huma.NewError(http.StatusUnprocessableEntity, "validation failed", errors.New("body.x: required"))
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("huma.NewError 回傳 %T，預期 *apierr.Error", err)
	}
	if e.Code != CodeInvalidInput || e.Message != "請求內容不正確。" || len(e.Details) != 1 {
		t.Errorf("e = %+v", e)
	}
}

func TestAsHidesUnknownErrors(t *testing.T) {
	if e := As(errors.New("secret detail")); e.Status != 500 || e.Code != CodeInternal || e.Message == "secret detail" {
		t.Errorf("As(未知錯誤) = %+v", e)
	}
	orig := New(409, "x", "y")
	if As(orig) != orig {
		t.Error("As 應該原樣回傳 *Error")
	}
}

func TestWriteOmitsEmptyFields(t *testing.T) {
	rec := httptest.NewRecorder()
	e := NotImplemented()
	e.RequiredPermission = "upload"
	Write(rec, e)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 501 || m["code"] != "not_implemented" || m["required_permission"] != "upload" {
		t.Errorf("回應 = %d %v", rec.Code, m)
	}
	if _, ok := m["required_scope"]; ok {
		t.Error("沒有設定的欄位不應該輸出")
	}
}
