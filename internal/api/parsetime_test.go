package api

import (
	"context"
	"testing"
)

func TestParseTimeRejectsNonFiniteAndNegative(t *testing.T) {
	for _, s := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "inf", "Infinity", "-1", "-0.5", "1e999", "1e8", "abc", "", "-00:00:01"} {
		if ms, err := parseTime(s); err == nil {
			t.Errorf("parseTime(%q) = %d，應回傳錯誤", s, ms)
		}
	}
	for s, want := range map[string]int64{"0": 0, "2": 2000, "2.5": 2500, "00:00:02": 2000, "1:02.5": 62500} {
		if ms, err := parseTime(s); err != nil || ms != want {
			t.Errorf("parseTime(%q) = %d, %v，預期 %d", s, ms, err, want)
		}
	}
}

// NaN 與 Inf 經過 API 也是 422，不會變成奇怪的範圍。
func TestCuesEndpointRejectsNonFiniteTimes(t *testing.T) {
	r := newItemsRig(t)
	id := decode[itemsBody](t, r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123"}`)).Items[0].ID
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"from=NaN", "to=NaN", "from=Inf", "to=%2BInf", "from=-Inf", "to=-0.5"} {
		if rec := r.owner("GET", "/api/items/"+id+"/cues?"+q, ""); rec.Code != 422 {
			t.Errorf("%s：%d %s", q, rec.Code, rec.Body)
		}
	}
}
