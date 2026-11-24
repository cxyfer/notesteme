package apierr

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestNegotiate(t *testing.T) {
	for h, want := range map[string]string{
		"":                             LangZhHant,
		"zh-Hant":                      LangZhHant,
		"zh-TW":                        LangZhHant,
		"zh-HK":                        LangZhHant,
		"zh-hant-tw":                   LangZhHant,
		"zh-Hans":                      LangZhHans,
		"zh-CN":                        LangZhHans,
		"zh-Hans-HK":                   LangZhHans,
		"zh":                           LangZhHant,
		"en":                           LangEn,
		"en-US,en;q=0.9":               LangEn,
		"EN-gb":                        LangEn,
		"fr":                           LangZhHant,
		"fr-FR,fr;q=0.9":               LangZhHant,
		"fr, en;q=0.5":                 LangEn,
		"*":                            LangZhHant,
		"en;q=0.2, zh-CN;q=0.8":        LangZhHans,
		"zh-CN;q=0.5, en;q=0.5":        LangZhHans, // q 相同時依出現的順序
		"en;q=0, zh-Hans":              LangZhHans,
		"en;q=0":                       LangZhHant,
		"en;q=abc":                     LangZhHant,
		"en;q=1.5":                     LangZhHant,
		"  en-US ; q=0.8 ,zh-TW;q=0.9": LangZhHant,
		";;,,":                         LangZhHant,
	} {
		if got := Negotiate(h); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", h, got, want)
		}
	}
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "en")
	if LangFrom(r) != LangEn {
		t.Error("LangFrom")
	}
}

func TestLocalize(t *testing.T) {
	e := New(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	e.ItemID = "item-1"
	en := Localize(e, LangEn)
	if en.Message != "This item was not found." || en.Code != CodeNotFound || en.ItemID != "item-1" || en.Status != http.StatusNotFound {
		t.Errorf("en = %+v", en)
	}
	if e.Message != "找不到這部影音。" {
		t.Error("Localize 不可以改到原本的錯誤")
	}
	if Localize(e, LangZhHant) != e || Localize(nil, LangEn) != nil {
		t.Error("繁中原樣回傳")
	}
	if got := Localize(New(400, "x", "目錄中沒有的訊息"), LangEn).Message; got != "目錄中沒有的訊息" {
		t.Errorf("不在目錄中的訊息照原樣：%q", got)
	}
	f := Newf(http.StatusForbidden, "forbidden", "forbidden_scope", "admin")
	if f.Message != "權限不足：需要 admin scope。" {
		t.Errorf("繁中 = %q", f.Message)
	}
	if got := Localize(f, LangZhHans).Message; got != "权限不足：需要 admin scope。" {
		t.Errorf("简中 = %q", got)
	}
	if got := Localize(f, LangEn).Message; got != "Permission denied: the admin scope is required." {
		t.Errorf("en = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("不存在的範本應該 panic")
		}
	}()
	Newf(400, "x", "no_such_template")
}

var placeholder = regexp.MustCompile(`\{\d+\}`)

// 目錄本身：每一則都有三種語言，範本的參數三種語言相同。
func TestCatalogComplete(t *testing.T) {
	for k, v := range messages {
		if strings.TrimSpace(v.hans) == "" || strings.TrimSpace(v.en) == "" {
			t.Errorf("%q 缺少翻譯", k)
		}
	}
	for k, v := range templates {
		want := placeholder.FindAllString(v.hant, -1)
		sort.Strings(want)
		for _, s := range []string{v.hans, v.en} {
			got := placeholder.FindAllString(s, -1)
			sort.Strings(got)
			if !slices.Equal(got, want) || s == "" {
				t.Errorf("範本 %s 的參數不一致：%q vs %q", k, v.hant, s)
			}
		}
	}
	for status, d := range DefaultMessages {
		if _, ok := messages[d.Message]; !ok {
			t.Errorf("DefaultMessages[%d] %q 不在目錄中", status, d.Message)
		}
	}
	if _, ok := messages[NotImplemented().Message]; !ok {
		t.Error("NotImplemented 的訊息不在目錄中")
	}
}

// internal/api 中直接寫成字串常值的錯誤訊息都要在目錄中（NFR-I18N-2）；不是常值的（domain 錯誤的 err.Error()、
// 字串串接）列出來，作為「尚未走目錄」的清單（見 nfr.md 第 10 節 NFR-I18N-2 的修訂紀錄）。
func TestAPIErrorLiteralsAreInCatalog(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("../api/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("找不到 internal/api：%v", err)
	}
	var literal, dynamic, newf int
	var notInCatalog, dynamicSites []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			idx := -1
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				switch fn.Name {
				case "newError":
					idx = 2
				case "errForbidden":
					idx = 0
				}
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok && x.Name == "apierr" && fn.Sel.Name == "New" {
					idx = 2
				}
				// 帶參數的訊息：範本名稱必須存在
				if x, ok := fn.X.(*ast.Ident); ok && x.Name == "apierr" && fn.Sel.Name == "Newf" && len(call.Args) > 2 {
					newf++
					if lit, ok := call.Args[2].(*ast.BasicLit); ok {
						if k, _ := strconv.Unquote(lit.Value); templates[k].hant == "" {
							t.Errorf("沒有這個訊息範本：%s", k)
						}
					}
				}
			}
			if idx < 0 || len(call.Args) <= idx {
				return true
			}
			pos := fset.Position(call.Pos())
			site := filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
			switch arg := call.Args[idx].(type) {
			case *ast.BasicLit:
				s, err := strconv.Unquote(arg.Value)
				if err != nil {
					t.Fatal(err)
				}
				literal++
				if _, ok := messages[s]; !ok {
					notInCatalog = append(notInCatalog, site+" "+s)
				}
			case *ast.Ident:
				// newError 與 errForbidden 自己的定義（參數 message）
				if arg.Name == "message" || arg.Name == "msg" && strings.HasPrefix(site, "errors.go") {
					return true
				}
				dynamic++
				dynamicSites = append(dynamicSites, site+" "+arg.Name)
			default:
				// defaultMessages[...].msg 是 DefaultMessages 的訊息，已在目錄中
				if sel, ok := arg.(*ast.SelectorExpr); ok && sel.Sel.Name == "msg" {
					if ix, ok := sel.X.(*ast.IndexExpr); ok {
						if id, ok := ix.X.(*ast.Ident); ok && id.Name == "defaultMessages" {
							literal++
							return true
						}
					}
				}
				dynamic++
				dynamicSites = append(dynamicSites, site+" "+exprString(fset, arg))
			}
			return true
		})
	}
	for _, s := range notInCatalog {
		t.Errorf("訊息不在目錄中（加進 internal/apierr/catalog.go 的 messages，或改用 Newf）：%s", s)
	}
	t.Logf("internal/api：字串常值 %d 處（都在目錄中）、帶參數的範本 %d 處、不是常值 %d 處", literal, newf, dynamic)
	for _, s := range dynamicSites {
		t.Logf("  未走目錄：%s", s)
	}
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	start, end := fset.Position(e.Pos()), fset.Position(e.End())
	if start.Filename != end.Filename {
		return "?"
	}
	src, err := readRange(start.Filename, start.Offset, end.Offset)
	if err != nil {
		return "?"
	}
	if len(src) > 80 {
		src = src[:80] + "…"
	}
	return src
}

func readRange(name string, start, end int) (string, error) {
	b, err := os.ReadFile(name)
	if err != nil || end > len(b) || start > end {
		return "", err
	}
	return string(b[start:end]), nil
}
