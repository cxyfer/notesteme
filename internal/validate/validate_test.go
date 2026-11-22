package validate

import (
	"reflect"
	"testing"
)

func TestPrefix(t *testing.T) {
	in := []FieldError{{Path: "language.targets", Message: "a"}, {Path: "", Message: "b"}}
	got := Prefix("profiles.broken", in)
	want := []FieldError{{Path: "profiles.broken.language.targets", Message: "a"}, {Path: "profiles.broken", Message: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Prefix = %+v，預期 %+v", got, want)
	}
	if in[0].Path != "language.targets" {
		t.Errorf("Prefix 不應修改輸入：%+v", in)
	}
	if Prefix("x", nil) != nil {
		t.Error("nil 輸入應回傳 nil")
	}
	if got := Prefix("", in); !reflect.DeepEqual(got, in) {
		t.Errorf("空前綴應原樣複製：%+v", got)
	}
}
