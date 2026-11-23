package steps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// 這個檔案放產出的來源紀錄（provenance）與 cue 時間來源的斷言，步驟的註冊在 items.go。

// errServerStopped 是來源紀錄的斷言在沒有執行中的 server 時的錯誤：它經 app.Server 的測試 hook 讀取，
// server 停止後不支援讀資料庫（見 liveStage）。
var errServerStopped = errors.New("server 已停止：來源紀錄的斷言需要執行中的 server，請在 server 執行時斷言")

// artifactHasProvenance 比對產出的來源紀錄；表格欄位 field、value，每一列都要符合。
//   - 讀取經 app.Server.ArtifactProvenance（只供測試用的 hook，不新增 API）。
//   - field 用點號表示巢狀；陣列的段落是索引（例如 params.0.lang）；值一律以字串比較。
//   - `parents` 是上游產出的變體名稱清單（不在 provenance_json 中），值以逗號分隔、依序比較。
func (s *Scenario) artifactHasProvenance(name, ref string, table *godog.Table) error {
	rows, err := provenanceRows(table)
	if err != nil {
		return err
	}
	// 場景讓 server 停下之後，讀 API 會隱含啟動新的 server（比照 liveStage，直接失敗）。
	if s.stopped {
		return errServerStopped
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	// itemID 會視需要隱含啟動 server；這裡仍沒有 server 時明確失敗，不要 nil pointer panic。
	if s.server == nil {
		return errServerStopped
	}
	rec, err := s.server.ArtifactProvenance(s.ctx, id, name)
	if err != nil {
		return fmt.Errorf("%s 的 %q 產出：%w", ref, name, err)
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(rec.Provenance))
	dec.UseNumber() // 保留數字原本的寫法，值以字串比較
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("%s 的 %q 產出：provenance 不是 JSON：%w（%s）", ref, name, err, snippet(rec.Provenance))
	}
	for _, row := range rows {
		got, err := provenanceValue(doc, rec.ParentVariants, row.field)
		if err != nil {
			return fmt.Errorf("%s 的 %q 產出：%w（provenance：%s）", ref, name, err, snippet(rec.Provenance))
		}
		if row.field == "parents" {
			if want := splitList(row.value); !slices.Equal(rec.ParentVariants, want) {
				return fmt.Errorf("%s 的 %q 產出：parents 是 %q，預期 %q", ref, name, rec.ParentVariants, want)
			}
			continue
		}
		if row.value == "*" {
			// `*` 代表欄位存在而且不是空值（例如 provider 的 batch ID，BATCH-R8；steps.md 4.4）。
			if got == "" || got == "null" {
				return fmt.Errorf("%s 的 %q 產出：%s 是空的，預期有值", ref, name, row.field)
			}
			continue
		}
		if got != row.value {
			return fmt.Errorf("%s 的 %q 產出：%s 是 %q，預期 %q", ref, name, row.field, got, row.value)
		}
	}
	return nil
}

type provenanceRow struct{ field, value string }

// provenanceRows 把表格（欄位 field、value；可有表頭）轉成各列。
func provenanceRows(table *godog.Table) ([]provenanceRow, error) {
	if table == nil || len(table.Rows) == 0 {
		return nil, fmt.Errorf("來源紀錄表格沒有內容")
	}
	var out []provenanceRow
	for i, r := range table.Rows {
		if len(r.Cells) != 2 {
			return nil, fmt.Errorf("表格第 %d 列應有 field 與 value 兩欄", i+1)
		}
		field, value := r.Cells[0].Value, r.Cells[1].Value
		if i == 0 && field == "field" && value == "value" {
			continue // 表頭
		}
		if field == "" {
			return nil, fmt.Errorf("表格第 %d 列的 field 是空的", i+1)
		}
		out = append(out, provenanceRow{field, value})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("來源紀錄表格沒有任何資料列")
	}
	return out, nil
}

// provenanceValue 依點號路徑取出值並轉成字串：
// 字串原樣；數字用原本的寫法；布林為 true、false；null 為 null；物件與陣列為精簡的 JSON。
// 第一段是 parents 時取上游變體清單（只有 `parents` 本身或加上索引）。路徑不存在時回傳錯誤。
func provenanceValue(doc any, parents []string, field string) (string, error) {
	segs := strings.Split(field, ".")
	cur := doc
	if segs[0] == "parents" {
		list := make([]any, len(parents))
		for i, p := range parents {
			list[i] = p
		}
		cur, segs = list, segs[1:]
		if len(segs) == 0 {
			return strings.Join(parents, ", "), nil
		}
	}
	for i, seg := range segs {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return "", fmt.Errorf("沒有欄位 %q（卡在 %q）", field, strings.Join(segs[:i+1], "."))
			}
			cur = next
		case []any:
			n, err := strconv.Atoi(seg)
			if err != nil || n < 0 || n >= len(v) {
				return "", fmt.Errorf("欄位 %q 的 %q 不是陣列（%d 個元素）的索引", field, seg, len(v))
			}
			cur = v[n]
		default:
			return "", fmt.Errorf("欄位 %q 在 %q 之前就是純量，沒有 %q", field, strings.Join(segs[:i], "."), seg)
		}
	}
	switch v := cur.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "null", nil
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// everyCueHasTiming 經 GET /api/items/{id}/cues 取得字幕，確認每個 cue 的時間來源都是指定的值。
// 沒有任何 cue 視為失敗（「所有 cue 都符合」不能因為沒有 cue 而恆真）。
func (s *Scenario) everyCueHasTiming(variant, ref, want string) error {
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	var got cuesJSON
	if err := s.apiJSON(http.MethodGet, "/api/items/"+url.PathEscape(id)+"/cues?"+url.Values{"variant": {variant}}.Encode(), nil, &got); err != nil {
		return fmt.Errorf("取得 %s 的 %s 字幕：%w", ref, variant, err)
	}
	return checkCueTiming(ref, variant, got, want)
}

func checkCueTiming(ref, variant string, got cuesJSON, want string) error {
	if len(got.Cues) == 0 {
		return fmt.Errorf("%s 的 %s 字幕（%s）沒有任何 cue，無法確認時間來源", ref, variant, got.Lang)
	}
	for i, c := range got.Cues {
		if c.Timing != want {
			return fmt.Errorf("%s 的 %s 字幕第 %d 句（共 %d 句）時間來源是 %q，預期所有 cue 都是 %q", ref, variant, i+1, len(got.Cues), c.Timing, want)
		}
	}
	return nil
}
