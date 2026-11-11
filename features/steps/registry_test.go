package steps

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

var (
	typeString    = reflect.TypeOf("")
	typeInt       = reflect.TypeOf(0)
	typeDocString = reflect.TypeOf((*godog.DocString)(nil))
	typeTable     = reflect.TypeOf((*godog.Table)(nil))
	typeError     = reflect.TypeOf((*error)(nil)).Elem()
)

// 每個定義的 handler 參數要和句型一致：{string}、{word} 為 string，{int} 為 int，
// 句型以 ":" 結尾時最後多一個 *godog.DocString 或 *godog.Table。
func TestHandlerSignatures(t *testing.T) {
	for _, d := range Definitions() {
		ht := reflect.TypeOf(d.handler)
		if ht == nil || ht.Kind() != reflect.Func {
			t.Errorf("%q: handler is not a func", d.Template)
			continue
		}
		var want []reflect.Type
		for _, g := range placeholderKinds(d.Template) {
			if g == "int" {
				want = append(want, typeInt)
			} else {
				want = append(want, typeString)
			}
		}
		got := make([]reflect.Type, ht.NumIn())
		if want == nil {
			want = []reflect.Type{}
		}
		for i := range got {
			got[i] = ht.In(i)
		}
		if strings.HasSuffix(d.Template, ":") {
			if len(got) == 0 || (got[len(got)-1] != typeDocString && got[len(got)-1] != typeTable) {
				t.Errorf("%q: last param must be *godog.DocString or *godog.Table, got %v", d.Template, got)
				continue
			}
			want = append(want, got[len(got)-1])
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: handler params = %v, want %v", d.Template, got, want)
		}
		if ht.NumOut() != 1 || ht.Out(0) != typeError {
			t.Errorf("%q: handler must return error", d.Template)
		}
		// regexp 的 capture group 數要等於前面的參數數量
		if n := d.Expr.NumSubexp(); n != len(placeholderKinds(d.Template)) {
			t.Errorf("%q: %d capture groups, want %d", d.Template, n, len(placeholderKinds(d.Template)))
		}
	}
}

func TestKeywordOnlyDefinitions(t *testing.T) {
	seen := map[string][]string{}
	for _, d := range Definitions() {
		seen[d.Template] = append(seen[d.Template], d.Keyword)
	}
	for tmpl, kws := range seen {
		switch {
		case len(kws) == 1 && kws[0] == "":
		case len(kws) == 2 && kws[0] == "Given" && kws[1] == "Then":
		default:
			t.Errorf("%q: unexpected keywords %q", tmpl, kws)
		}
	}
}

func placeholderKinds(template string) []string {
	var kinds []string
	for {
		i := strings.Index(template, "{")
		if i < 0 {
			return kinds
		}
		j := strings.Index(template[i:], "}")
		kinds = append(kinds, template[i+1:i+j])
		template = template[i+j+1:]
	}
}
