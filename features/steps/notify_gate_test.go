package steps

import (
	"testing"

	"github.com/cucumber/godog"
)

// 通知的閘門只對 NOTIFY 的場景放行（M4-S1）；共用句型的其他場景維持裸的 godog.ErrPending
// （junit 的訊息與切片之前相同）。
func TestNotifyGate(t *testing.T) {
	cases := []struct {
		tags    []string
		pending bool
	}{
		{[]string{"@M4", "@NOTIFY-01", "@NOTIFY-R1"}, false},
		{[]string{"@M4", "@NOTIFY-01", "@NOTIFY-R7", "@NOTIFY-R12"}, false},
		{[]string{"@M4", "@DIG-01", "@DIG-R9"}, false},  // M4-S6：DIG 的場景也放行
		{[]string{"@M4", "@DIG-01"}, true},              // 沒有 @DIG-R* 的不算
		{[]string{"@M4", "@OPS-07", "@OPS-R13"}, false}, // M4 OPS：OPS-R11～R21 的場景也放行
		{[]string{"@M4", "@OPS-05", "@OPS-R9"}, true},   // M1 的 OPS 規則不算
		{[]string{"@M4", "@NOTIFY"}, true},              // 沒有 - 的不算
		{nil, true},
	}
	for _, c := range cases {
		s := &Scenario{tags: c.tags}
		err := s.notifyGate()
		if (err != nil) != c.pending {
			t.Errorf("%v：err = %v，預期 pending=%v", c.tags, err, c.pending)
		}
		if c.pending && err != godog.ErrPending {
			t.Errorf("%v：要回傳裸的 godog.ErrPending，實際 %v", c.tags, err)
		}
	}
	// 閘門也擋住其他 NOTIFY 句型：非 NOTIFY 場景不登記 notifier。
	s := &Scenario{tags: []string{"@OPS-05", "@OPS-R9"}}
	if err := s.notifierIs("x", &godog.DocString{Content: "{}"}); err != godog.ErrPending || len(s.notifiers) != 0 {
		t.Errorf("非 NOTIFY 場景的 notifier：%v %v", err, s.notifiers)
	}
}
