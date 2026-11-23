package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// 真 Hub 的閘門只對登入範圍的 HACC 場景放行（M5-S1：R1～R8，R10 只能和它們一起出現）；
// 其他規則、HREP、HMCP 的場景維持 pending，而且是裸的 ErrPending（junit 的訊息與切片之前相同）。
func TestHubGate(t *testing.T) {
	cases := []struct {
		tags    []string
		pending bool
	}{
		{[]string{"@M5", "@HUB-01", "@HACC-R1"}, false},
		{[]string{"@M5", "@HUB-01", "@HACC-R2", "@HACC-R3"}, false},
		{[]string{"@M5", "@HUB-01", "@HACC-R4", "@HACC-R10"}, false},
		{[]string{"@M5", "@HUB-01", "@HACC-R8"}, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R9"}, true},
		{[]string{"@M5", "@HUB-07", "@HACC-R10", "@HACC-R11"}, true},
		{[]string{"@M5", "@HUB-01", "@HACC-R6", "@HACC-R17"}, true}, // 混了 S1 以外的規則
		{[]string{"@M5", "@HUB-07", "@HACC-R12", "@HACC-R13"}, true},
		{[]string{"@M5", "@HUB-02", "@HREP-R1"}, true},
		{[]string{"@M5", "@HUB-05", "@HMCP-R1"}, true},
		{nil, true},
	}
	for _, c := range cases {
		s := &Scenario{tags: c.tags}
		err := s.hubGate()
		if errors.Is(err, godog.ErrPending) != c.pending {
			t.Errorf("%v：err = %v，預期 pending=%v", c.tags, err, c.pending)
		}
		if c.pending && err != godog.ErrPending {
			t.Errorf("%v：要回傳裸的 godog.ErrPending，實際 %v", c.tags, err)
		}
	}
}
