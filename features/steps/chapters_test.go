package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// SponsorBlock 的句型只對 SPB 的場景放行；SPB-R7 在 note 步驟實作之前也是 pending。
func TestSPBGate(t *testing.T) {
	cases := []struct {
		tags    []string
		pending bool
	}{
		{[]string{"@M3", "@SPB-01", "@SPB-R1"}, false},
		{[]string{"@M3", "@CHAP-01", "@CHAP-R5"}, true},
		{[]string{"@M3", "@FRM-01"}, true},
		{[]string{"@M4", "@SRC-10", "@SRC-R20"}, true},
		{[]string{"@M3", "@SPB-01", "@SPB-R7"}, true},
		{nil, true},
	}
	for _, c := range cases {
		s := &Scenario{tags: c.tags}
		if err := s.spbGate(); errors.Is(err, godog.ErrPending) != c.pending {
			t.Errorf("%v：err = %v，預期 pending=%v", c.tags, err, c.pending)
		}
	}
}

func TestSponsorBlockVideoID(t *testing.T) {
	if id, err := sponsorBlockVideoID("yt:spon01xxxxx"); err != nil || id != "spon01xxxxx" {
		t.Errorf("yt：%q %v", id, err)
	}
	for _, ref := range []string{"bili:BV1sp4y1c7mD/p1", "yt:short", "spon01xxxxx"} {
		if _, err := sponsorBlockVideoID(ref); err == nil {
			t.Errorf("%s 應該失敗", ref)
		}
	}
}
