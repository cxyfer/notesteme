package prompts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestBodySHA256(t *testing.T) {
	// printf 'abc' | sha256sum
	if got, want := BodySHA256("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("BodySHA256 = %s，預期 %s", got, want)
	}
}

func TestContractLists(t *testing.T) {
	// M3 修訂（PRM）：kind 的 frames.select 改為 frames，另加 note_map；M3 的內建 prompt 由實作切片加入。
	// M3-S2 加入 chapters-default（CHAP H6），M3-S3 加入 frames-default。
	if len(Kinds) != 8 || len(Variables) != 7 || len(BuiltinIDs) != 7 {
		t.Errorf("Kinds=%d Variables=%d BuiltinIDs=%d，契約是 8、7、7", len(Kinds), len(Variables), len(BuiltinIDs))
	}
	for _, k := range []string{"chapters", "frames", "note", "note_map"} {
		if !slices.Contains(Kinds, k) {
			t.Errorf("Kinds 缺少 M3 的 %s", k)
		}
	}
}

// newService 建立接上暫存資料庫的 Service；usedBy 為 nil 代表沒有人引用。
func newService(t *testing.T, usedBy func(context.Context, *store.Repo, string) ([]string, error)) *Service {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(Deps{Store: st, Clock: clk, UsedBy: usedBy})
}

// 內建 prompt 都能載入：類型、版本、變數都在契約內，內文的 sha256 與內容一致。
func TestBuiltinsLoad(t *testing.T) {
	wantKind := map[string]string{
		"segment-default": "segment", "correct-default": "correct", "translate-default": "translate",
		"localize-zh-hant-tw": "translate", "localize-zh-hans-cn": "translate", "chapters-default": "chapters", "frames-default": "frames",
	}
	for _, id := range BuiltinIDs {
		b, ok := builtins[id]
		if !ok {
			t.Fatalf("缺少內建 prompt %s", id)
		}
		if !b.prompt.Builtin || b.prompt.Kind != wantKind[id] || b.prompt.DefaultVersion != len(b.versions) {
			t.Errorf("%s 的摘要 = %+v", id, b.prompt)
		}
		for _, v := range b.versions {
			if strings.TrimSpace(v.Body) == "" || strings.Contains(v.Body, "\r") {
				t.Errorf("%s v%d 的內文是空的或含 CR", id, v.Version)
			}
			if v.SHA256 != BodySHA256(v.Body) {
				t.Errorf("%s v%d 的 sha256 不一致", id, v.Version)
			}
			for _, name := range v.Variables {
				if !slices.Contains(Variables, name) {
					t.Errorf("%s v%d 用到未定義的變數 %s", id, v.Version, name)
				}
			}
		}
	}
}

func TestParseRef(t *testing.T) {
	ok := map[string]Ref{
		"correct-default":         {ID: "correct-default"},
		"correct-default@default": {ID: "correct-default"},
		"my-correct@1":            {ID: "my-correct", Version: 1},
		"my.prompt@12":            {ID: "my.prompt", Version: 12},
	}
	for in, want := range ok {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %+v, %v；預期 %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "x@0", "x@", "@1", "x@abc", "x@-1", "x@+1", "x@01", "x@1@2", "X@1", "x y@1", "x@default2", "x@99999999999999999999"} {
		_, err := ParseRef(in)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("ParseRef(%q) err = %v，預期 *InvalidError", in, err)
			continue
		}
		if len(inv.Fields) != 1 || inv.Fields[0].Path != "prompt" {
			t.Errorf("ParseRef(%q) 的欄位錯誤 = %+v", in, inv.Fields)
		}
	}
}

func TestParseVariables_Undefined(t *testing.T) {
	used, undefined, err := ParseVariables("標題：{{titel}}，{{ title }}，{{glossary}}，{{titel}}，{{ foo_bar }}")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"titel", "title", "glossary", "foo_bar"}; !slices.Equal(used, want) {
		t.Errorf("used = %v，預期 %v", used, want)
	}
	if want := []string{"titel", "foo_bar"}; !slices.Equal(undefined, want) {
		t.Errorf("undefined = %v，預期 %v", undefined, want)
	}
	// 每個契約內的變數都是已定義的。
	for _, name := range Variables {
		if _, undefined, err := ParseVariables("{{" + name + "}}"); err != nil || len(undefined) != 0 {
			t.Errorf("{{%s}}：undefined = %v, err = %v", name, undefined, err)
		}
	}
	// 沒有變數：兩個都是空的（不是 nil，方便序列化成 []）。
	if used, undefined, err := ParseVariables("沒有變數 { } {x}"); err != nil || used == nil || undefined == nil || len(used)+len(undefined) != 0 {
		t.Errorf("沒有變數：%v %v %v", used, undefined, err)
	}
}

func TestParseVariables_Unclosed(t *testing.T) {
	for _, body := range []string{"標題：{{title", "{{ title }} 與 {{glossary", "{{}}", "{{ a b }}", "{{\n}}"} {
		if _, _, err := ParseVariables(body); err == nil {
			t.Errorf("ParseVariables(%q) 沒有回傳錯誤", body)
		}
	}
}

func TestRender(t *testing.T) {
	got, err := Render("T:{{ title }}/{{glossary}}/{{title}}", map[string]string{"title": "A", "glossary": "{{title}}"})
	if err != nil || got != "T:A/{{title}}/A" {
		t.Errorf("Render = %q, %v", got, err)
	}
	if got, err := Render("沒有變數", nil); err != nil || got != "沒有變數" {
		t.Errorf("Render 沒有變數 = %q, %v", got, err)
	}
	if _, err := Render("{{title}}", map[string]string{}); err == nil {
		t.Error("缺少變數的值應回傳錯誤")
	}
	if _, err := Render("{{title", map[string]string{"title": "A"}); err == nil {
		t.Error("未關閉的 {{ 應回傳錯誤")
	}
}

func TestCreate_KindRejectedPath(t *testing.T) {
	s := newService(t, nil)
	_, _, err := s.Create(t.Context(), "my-poem", "poem", "寫一首詩")
	var inv *InvalidError
	if !errors.As(err, &inv) || len(inv.Fields) == 0 || inv.Fields[0].Path != "kind" {
		t.Fatalf("err = %v，預期第一個欄位錯誤的 path 是 kind", err)
	}
	// 同時有多個錯誤時依 id → kind → body 排列。
	_, _, err = s.Create(t.Context(), "Bad ID", "poem", " ")
	if !errors.As(err, &inv) {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range inv.Fields {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"id", "kind", "body"}) {
		t.Errorf("錯誤路徑 = %v，預期 [id kind body]", paths)
	}
	// 沒有建立任何東西。
	if ps, _ := s.List(t.Context()); len(ps) != len(BuiltinIDs) {
		t.Errorf("失敗之後有 %d 個 prompt，預期只有內建的 %d 個", len(ps), len(BuiltinIDs))
	}
}

func TestCreate_Basics(t *testing.T) {
	ctx := t.Context()
	s := newService(t, nil)
	p, v, err := s.Create(ctx, "my-correct", "correct", "你是校對員。\r\n詞彙表：{{ glossary }}")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "correct" || p.Builtin || p.DefaultVersion != 1 || p.LatestVersion != 1 {
		t.Errorf("Prompt = %+v", p)
	}
	// CRLF 正規化成 LF，sha256 對正規化後的內文計算；變數記下來。
	if v.Version != 1 || v.Body != "你是校對員。\n詞彙表：{{ glossary }}" || v.SHA256 != BodySHA256(v.Body) || !slices.Equal(v.Variables, []string{"glossary"}) {
		t.Errorf("Version = %+v", v)
	}
	if _, _, err := s.Create(ctx, "my-correct", "correct", "x"); !errors.Is(err, ErrExists) {
		t.Errorf("重複建立：err = %v，預期 ErrExists", err)
	}
	// 內文的檢查：空的、太大、未定義的變數、未關閉。
	for name, body := range map[string]string{
		"空的": " \n", "太大": strings.Repeat("a", maxBodyBytes+1), "未定義": "{{titel}}", "未關閉": "{{title",
	} {
		_, _, err := s.Create(ctx, "other", "correct", body)
		var inv *InvalidError
		if !errors.As(err, &inv) || inv.Fields[0].Path != "body" {
			t.Errorf("%s：err = %v，預期 body 的 *InvalidError", name, err)
		}
	}
	// 上限剛好是可以的。
	if _, _, err := s.Create(ctx, "big", "note", strings.Repeat("a", maxBodyBytes)); err != nil {
		t.Errorf("剛好 64 KiB：%v", err)
	}
	// 未定義的變數會列在 Undefined。
	_, _, err = s.Create(ctx, "other", "correct", "{{titel}} {{x}} {{titel}}")
	var inv *InvalidError
	if !errors.As(err, &inv) || !slices.Equal(inv.Undefined, []string{"titel", "x"}) {
		t.Errorf("Undefined = %v", inv)
	}
}

func TestList_BuiltinsFirst(t *testing.T) {
	ctx := t.Context()
	s := newService(t, nil)
	for _, id := range []string{"zeta", "alpha"} {
		if _, _, err := s.Create(ctx, id, "note", "x"); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	want := []string{"chapters-default", "correct-default", "frames-default", "localize-zh-hans-cn", "localize-zh-hant-tw", "segment-default", "translate-default", "alpha", "zeta"}
	if !slices.Equal(ids, want) {
		t.Errorf("List = %v，預期 %v", ids, want)
	}
	if !ps[0].Builtin || ps[len(ps)-1].Builtin {
		t.Error("Builtin 旗標不正確")
	}
}

func TestAddVersion_DoesNotChangeDefault(t *testing.T) {
	ctx := t.Context()
	s := newService(t, nil)
	if _, _, err := s.Create(ctx, "my-correct", "correct", "v1 {{glossary}}"); err != nil {
		t.Fatal(err)
	}
	v, err := s.AddVersion(ctx, "my-correct", "v2 {{glossary}}")
	if err != nil || v.Version != 2 {
		t.Fatalf("AddVersion = %+v, %v", v, err)
	}
	// 內容相同也是新版本（PRM-R2）。
	if v, err := s.AddVersion(ctx, "my-correct", "v2 {{glossary}}"); err != nil || v.Version != 3 {
		t.Fatalf("內容相同的 AddVersion = %+v, %v", v, err)
	}
	ps, _ := s.List(ctx)
	var mine Prompt
	for _, p := range ps {
		if p.ID == "my-correct" {
			mine = p
		}
	}
	if mine.DefaultVersion != 1 || mine.LatestVersion != 3 {
		t.Errorf("Prompt = %+v，預期 default 仍是 1、latest 是 3", mine)
	}
	// 沒有 @版本的引用仍然解析到預設的版本 1。
	if r, err := s.Resolve(ctx, "my-correct"); err != nil || r.Version != 1 {
		t.Errorf("Resolve = %+v, %v", r, err)
	}
	vs, err := s.Versions(ctx, "my-correct")
	if err != nil || len(vs) != 3 || vs[0].Version != 1 || vs[2].Version != 3 {
		t.Errorf("Versions = %+v, %v", vs, err)
	}
	// 未定義的變數、不存在的 prompt。
	_, err = s.AddVersion(ctx, "my-correct", "標題：{{titel}}")
	var inv *InvalidError
	if !errors.As(err, &inv) || !slices.Equal(inv.Undefined, []string{"titel"}) {
		t.Errorf("未定義的變數：err = %v", err)
	}
	if vs, _ := s.Versions(ctx, "my-correct"); len(vs) != 3 {
		t.Errorf("失敗的 AddVersion 不該留下版本，現在有 %d 個", len(vs))
	}
	if _, err := s.AddVersion(ctx, "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 prompt：err = %v", err)
	}
	if _, err := s.Versions(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 prompt 的 Versions：err = %v", err)
	}
}

func TestBuiltin_Immutable409(t *testing.T) {
	ctx := t.Context()
	s := newService(t, func(context.Context, *store.Repo, string) ([]string, error) { return nil, nil })
	for _, id := range BuiltinIDs {
		if _, err := s.AddVersion(ctx, id, "something else"); !errors.Is(err, ErrBuiltin) {
			t.Errorf("AddVersion(%s) err = %v，預期 ErrBuiltin", id, err)
		}
		if _, err := s.SetDefault(ctx, id, 1); !errors.Is(err, ErrBuiltin) {
			t.Errorf("SetDefault(%s) err = %v，預期 ErrBuiltin", id, err)
		}
		if err := s.Delete(ctx, id); !errors.Is(err, ErrBuiltin) {
			t.Errorf("Delete(%s) err = %v，預期 ErrBuiltin", id, err)
		}
		// 使用者的 prompt id 不能和內建的相同。
		if _, _, err := s.Create(ctx, id, "correct", "x"); !errors.Is(err, ErrBuiltin) {
			t.Errorf("Create(%s) err = %v，預期 ErrBuiltin", id, err)
		}
		// 內建的版本可以讀。
		if vs, err := s.Versions(ctx, id); err != nil || len(vs) == 0 {
			t.Errorf("Versions(%s) = %+v, %v", id, vs, err)
		}
	}
}

func TestSetDefault_UnknownVersion422(t *testing.T) {
	ctx := t.Context()
	s := newService(t, nil)
	if _, _, err := s.Create(ctx, "p", "correct", "x"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, -1, 2} {
		_, err := s.SetDefault(ctx, "p", n)
		var inv *InvalidError
		if !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != "version" {
			t.Errorf("SetDefault(%d) err = %v，預期 path=version 的 *InvalidError", n, err)
		}
	}
	if _, err := s.SetDefault(ctx, "nope", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 prompt：err = %v", err)
	}
	if r, _ := s.Resolve(ctx, "p"); r.Version != 1 {
		t.Errorf("失敗的 SetDefault 不該改變預設，現在是 %d", r.Version)
	}
	if _, err := s.AddVersion(ctx, "p", "y"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.SetDefault(ctx, "p", 2); err != nil || p.DefaultVersion != 2 || p.LatestVersion != 2 {
		t.Errorf("SetDefault(2) = %+v, %v", p, err)
	}
	// 可以回到舊的版本。
	if p, err := s.SetDefault(ctx, "p", 1); err != nil || p.DefaultVersion != 1 {
		t.Errorf("SetDefault(1) = %+v, %v", p, err)
	}
}

func TestDelete_InUse409UsedBy(t *testing.T) {
	ctx := t.Context()
	users := []string{"standard", "settings"}
	var asked []string
	s := newService(t, func(_ context.Context, _ *store.Repo, id string) ([]string, error) {
		asked = append(asked, id)
		return users, nil
	})
	if _, _, err := s.Create(ctx, "my-correct", "correct", "x"); err != nil {
		t.Fatal(err)
	}
	err := s.Delete(ctx, "my-correct")
	var iu *InUseError
	if !errors.As(err, &iu) || !slices.Equal(iu.UsedBy, users) {
		t.Fatalf("Delete err = %v，預期 *InUseError{UsedBy: %v}", err, users)
	}
	if !slices.Equal(asked, []string{"my-correct"}) {
		t.Errorf("UsedBy 被問了 %v", asked)
	}
	if _, err := s.Versions(ctx, "my-correct"); err != nil {
		t.Errorf("被擋下的刪除不該動到 prompt：%v", err)
	}

	// 沒有人引用之後可以刪，版本一起刪；再刪是 ErrNotFound，同 id 可以重新建立。
	users = nil
	if err := s.Delete(ctx, "my-correct"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "my-correct"); !errors.Is(err, ErrNotFound) {
		t.Errorf("重複刪除：err = %v，預期 ErrNotFound", err)
	}
	if _, err := s.Resolve(ctx, "my-correct"); !errors.Is(err, ErrNotFound) {
		t.Errorf("刪除後 Resolve：err = %v", err)
	}
	if _, v, err := s.Create(ctx, "my-correct", "correct", "again"); err != nil || v.Version != 1 {
		t.Errorf("刪除後重新建立 = %+v, %v", v, err)
	}
}

// P7：讀取使用者清單與刪除在同一個寫入交易內。檢查通過之後、刪除之前，另一個連線想新增引用（寫入設定）
// 必須被擋住，要等刪除結束才寫得進去；檢查若在交易外，這個寫入會立刻成功，引用就在檢查與刪除之間冒出來。
func TestDelete_CheckAndDeleteShareOneWriteTx(t *testing.T) {
	ctx := t.Context()
	var s *Service
	written := make(chan error, 1)
	var duringCheck bool
	s = newService(t, func(_ context.Context, _ *store.Repo, id string) ([]string, error) {
		// 檢查回傳「沒有人用」之後，另一個連線立刻想新增引用。
		go func() {
			written <- s.st.Write().SetSetting(context.Background(), "settings.stages", `{"stages":{"correct":{"prompt":"`+id+`"}}}`)
		}()
		select {
		case err := <-written:
			duringCheck = true
			written <- err
		case <-time.After(200 * time.Millisecond):
		}
		return nil, nil
	})
	if _, _, err := s.Create(ctx, "my-correct", "correct", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "my-correct"); err != nil {
		t.Fatal(err)
	}
	if duringCheck {
		t.Fatal("檢查與刪除之間，另一個連線的寫入沒有被擋住：檢查不在刪除的交易內")
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("交易結束後，被擋住的寫入應該完成：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("交易結束後，被擋住的寫入沒有完成（寫入連線沒有釋放？）")
	}
}

// 檢查回報有人引用時整個交易回復：交易內的其他寫入也不留下，prompt 與寫入連線都完好。
func TestDelete_InUseRollsBackTx(t *testing.T) {
	ctx := t.Context()
	s := newService(t, func(ctx context.Context, r *store.Repo, id string) ([]string, error) {
		// r 是交易的 Repo：寫得進去、讀得到自己的寫入（不是唯讀連線，也不是另一條連線）。
		if err := r.SetSetting(ctx, "probe", "in-tx"); err != nil {
			return nil, err
		}
		if v, ok, err := r.Setting(ctx, "probe"); err != nil || !ok || v != "in-tx" {
			t.Errorf("r 看不到交易內的寫入：%q, %v, %v", v, ok, err)
		}
		return []string{"item:yt:abc123xxxxx"}, nil
	})
	if _, _, err := s.Create(ctx, "p", "correct", "x"); err != nil {
		t.Fatal(err)
	}
	var iu *InUseError
	if err := s.Delete(ctx, "p"); !errors.As(err, &iu) || !slices.Equal(iu.UsedBy, []string{"item:yt:abc123xxxxx"}) {
		t.Fatalf("Delete err = %v，預期 *InUseError", err)
	}
	if _, ok, err := s.st.Read().Setting(ctx, "probe"); err != nil || ok {
		t.Errorf("回復後不該留下交易內的寫入：ok=%v err=%v", ok, err)
	}
	if err := s.st.Write().SetSetting(ctx, "after", "ok"); err != nil {
		t.Errorf("回復後寫入連線應該已釋放：%v", err)
	}
}

// UsedBy 失敗時刪除也失敗，不能當成沒有人引用。
func TestDelete_UsedByErrorBlocks(t *testing.T) {
	ctx := t.Context()
	boom := errors.New("boom")
	s := newService(t, func(context.Context, *store.Repo, string) ([]string, error) { return nil, boom })
	if _, _, err := s.Create(ctx, "p", "correct", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "p"); !errors.Is(err, boom) {
		t.Errorf("err = %v，預期 boom", err)
	}
	if _, err := s.Resolve(ctx, "p"); err != nil {
		t.Errorf("prompt 不該被刪掉：%v", err)
	}
}

func TestResolve_DefaultFollowsPromotion(t *testing.T) {
	ctx := t.Context()
	s := newService(t, nil)
	_, v1, err := s.Create(ctx, "my-correct", "correct", "舊 {{glossary}}")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.AddVersion(ctx, "my-correct", "新 {{glossary}}")
	if err != nil {
		t.Fatal(err)
	}

	// 升級之前：id 與 @default 都是版本 1；@2 固定在版本 2。
	for ref, want := range map[string]Version{"my-correct": v1, "my-correct@default": v1, "my-correct@1": v1, "my-correct@2": v2} {
		r, err := s.Resolve(ctx, ref)
		if err != nil || r.Version != want.Version || r.SHA256 != want.SHA256 || r.Body != want.Body || r.Kind != "correct" || r.ID != "my-correct" {
			t.Errorf("Resolve(%q) = %+v, %v；預期版本 %d", ref, r, err, want.Version)
		}
	}
	// 升級之後：id 與 @default 跟著走，@1 不動。
	if _, err := s.SetDefault(ctx, "my-correct", 2); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]int{"my-correct": 2, "my-correct@default": 2, "my-correct@1": 1} {
		if r, err := s.Resolve(ctx, ref); err != nil || r.Version != want {
			t.Errorf("升級後 Resolve(%q) = %+v, %v；預期版本 %d", ref, r, err, want)
		}
	}

	if _, err := s.Resolve(ctx, "my-correct@3"); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("不存在的版本：err = %v，預期 ErrVersionNotFound", err)
	}
	if _, err := s.Resolve(ctx, "nope@1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 prompt：err = %v，預期 ErrNotFound", err)
	}
	var inv *InvalidError
	if _, err := s.Resolve(ctx, "my-correct@0"); !errors.As(err, &inv) {
		t.Errorf("格式錯誤：err = %v，預期 *InvalidError", err)
	}
}

func TestResolve_BuiltinSHA(t *testing.T) {
	ctx := t.Context()
	s := newService(t, nil)
	for _, id := range BuiltinIDs {
		want := builtins[id].versions[0]
		for _, ref := range []string{id, id + "@default", id + "@1"} {
			r, err := s.Resolve(ctx, ref)
			if err != nil {
				t.Fatalf("Resolve(%q)：%v", ref, err)
			}
			if r.Version != 1 || r.Kind != builtins[id].prompt.Kind || r.Body != want.Body || r.SHA256 != BodySHA256(r.Body) || r.SHA256 != want.SHA256 {
				t.Errorf("Resolve(%q) = %+v", ref, r)
			}
		}
		if _, err := s.Resolve(ctx, id+"@2"); !errors.Is(err, ErrVersionNotFound) {
			t.Errorf("Resolve(%s@2) err = %v，預期 ErrVersionNotFound", id, err)
		}
	}
}
