package steps

import (
	"strings"
	"testing"
)

func TestJSONSubset(t *testing.T) {
	tests := []struct {
		name, want, got string
		errSub          string // 空字串表示應該通過
	}{
		{"完全相同", `{"a":1}`, `{"a":1}`, ""},
		{"多餘的欄位", `{"a":1}`, `{"a":1,"b":2}`, ""},
		{"巢狀物件", `{"a":{"b":"x"}}`, `{"a":{"b":"x","c":1},"d":2}`, ""},
		{"缺欄位", `{"a":1}`, `{"b":1}`, "$.a"},
		{"值不同", `{"a":1}`, `{"a":2}`, "預期 1，實際 2"},
		{"型別不同", `{"a":"1"}`, `{"a":1}`, "$.a"},
		{"* 欄位存在", `{"id":"*"}`, `{"id":"01HX"}`, ""},
		{"* 允許 null", `{"id":"*"}`, `{"id":null}`, ""},
		{"* 欄位仍要存在", `{"id":"*"}`, `{}`, "$.id"},
		{"陣列前 N 個", `{"l":["a","b"]}`, `{"l":["a","b","c"]}`, ""},
		{"陣列順序", `{"l":["b","a"]}`, `{"l":["a","b"]}`, "$.l[0]"},
		{"陣列太短", `{"l":["a","b"]}`, `{"l":["a"]}`, "至少 2 個"},
		{"陣列元素是物件", `{"l":[{"n":1}]}`, `{"l":[{"n":1,"m":2},{"n":3}]}`, ""},
		{"預期物件但是陣列", `{"a":{}}`, `{"a":[]}`, "預期是物件"},
		{"空陣列", `{"items":[]}`, `{"items":[]}`, ""},
		{"回應不是 JSON", `{"a":1}`, `nope`, "不是合法的 JSON"},
		{"預期不是 JSON", `{`, `{}`, "預期內容"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := jsonSubset([]byte(tt.want), []byte(tt.got))
			switch {
			case tt.errSub == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.errSub != "" && err == nil:
				t.Errorf("want error containing %q, got nil", tt.errSub)
			case tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub):
				t.Errorf("error %q does not contain %q", err, tt.errSub)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]string{"61s": "1m1s", "25h": "25h0m0s", "11m": "11m0s", "2d": "48h0m0s"} {
		d, err := parseDuration(in)
		if err != nil || d.String() != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %s", in, d, err, want)
		}
	}
	if _, err := parseDuration("soon"); err == nil {
		t.Error("parseDuration(soon) should fail")
	}
}
