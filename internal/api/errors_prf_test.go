package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
)

// mapErr 對 PRF／PRM 新增的錯誤：422 帶 errors、409 帶 used_by 與各自的 code、404 not_found。
func TestMapErr_ProfileAndPromptErrors(t *testing.T) {
	rt := &routes{}
	fields := []validate.FieldError{{Path: "stages.correct.provider", Message: "executor 為 api 時必須指定 provider。"}, {Path: "stages.correct.model", Message: "x"}}
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		check  func(t *testing.T, e *Error)
	}{
		{"invalid with fields", &pipeline.InvalidError{Msg: "m", Fields: fields}, 422, CodeInvalidInput, func(t *testing.T, e *Error) {
			if !reflect.DeepEqual(e.Errors, fields) || e.Message != "m" {
				t.Errorf("errors/message = %+v / %q", e.Errors, e.Message)
			}
		}},
		{"invalid without fields stays as M1", &pipeline.InvalidError{Msg: "m"}, 422, CodeInvalidInput, func(t *testing.T, e *Error) {
			if e.Errors != nil {
				t.Errorf("沒有 Fields 時不應有 errors：%+v", e.Errors)
			}
		}},
		{"profile in use", &pipeline.InUseError{Code: pipeline.CodeProfileInUse, UsedBy: []string{"default_profile"}}, 409, "profile_in_use", func(t *testing.T, e *Error) {
			if !reflect.DeepEqual(e.UsedBy, []string{"default_profile"}) {
				t.Errorf("used_by = %v", e.UsedBy)
			}
		}},
		{"prompt in use", &pipeline.InUseError{Code: pipeline.CodePromptInUse, UsedBy: []string{"standard"}}, 409, "prompt_in_use", nil},
		{"profile not found", pipeline.ErrProfileNotFound, 404, CodeNotFound, nil},
		{"prompt not found", pipeline.ErrPromptNotFound, 404, CodeNotFound, nil},
		{"prompt version not found", pipeline.ErrPromptVersionNotFound, 404, CodeNotFound, nil},
		{"profile exists", pipeline.ErrProfileExists, 409, "profile_exists", nil},
		{"builtin profile", pipeline.ErrBuiltinProfile, 409, "builtin_profile", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := asError(rt.mapErr(context.Background(), c.err))
			if e.Status != c.status || e.Code != c.code {
				t.Fatalf("= %d %s，預期 %d %s", e.Status, e.Code, c.status, c.code)
			}
			if c.check != nil {
				c.check(t, e)
			}
		})
	}
}

// 新欄位沒有值時不出現在 JSON（只增不改）。
func TestError_NewFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(newError(http.StatusUnprocessableEntity, CodeInvalidInput, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"code":"invalid_request","message":"x"}` {
		t.Errorf("JSON = %s", b)
	}
}
