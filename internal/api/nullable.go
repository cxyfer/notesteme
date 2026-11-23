package api

import "github.com/danielgtaylor/huma/v2"

// nullableRefs 把物件型別欄位的 schema 改成「該物件或 null」：Huma 的 nullable tag 只支援純量，
// 而 reprobe、better_source（SRC-R18、R23）沒有值時要明確回傳 null（欄位仍然必有）。
func nullableRefs(api huma.API, schema string, fields ...string) {
	s := api.OpenAPI().Components.Schemas.Map()[schema]
	if s == nil {
		return
	}
	for _, f := range fields {
		p := s.Properties[f]
		if p == nil || p.Ref == "" {
			continue
		}
		s.Properties[f] = &huma.Schema{
			Description: p.Description,
			OneOf:       []*huma.Schema{{Ref: p.Ref}, {Type: "null"}},
		}
	}
}
