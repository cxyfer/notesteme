package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// SponsorBlock 的句型只對 SPB、CHAP（M3-S2）、FRM（M3-S3，FRM-R6）與 NOTE（M3-S4）的場景放行；SPB-R7 在 note 步驟實作之後放行。
func TestSPBGate(t *testing.T) {
	cases := []struct {
		tags    []string
		pending bool
	}{
		{[]string{"@M3", "@SPB-01", "@SPB-R1"}, false},
		{[]string{"@M3", "@CHAP-01", "@CHAP-R5"}, false}, // M3-S2：chapters 已實作
		{[]string{"@M3", "@FRM-02", "@FRM-R6"}, false},
		{[]string{"@M3", "@NOTE-01"}, false}, // M3-S4：note 已實作
		{[]string{"@M3", "@GEM-01"}, true},
		{[]string{"@M3", "@VAULT-01"}, true},
		{[]string{"@M4", "@SRC-10", "@SRC-R20"}, true},
		{[]string{"@M3", "@SPB-01", "@SPB-R7"}, false}, // M3-S4：note 已實作
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

// 畫面的句型只對 FRM 的場景放行（M3-S3）。
func TestFRMGate(t *testing.T) {
	for tags, pending := range map[string]bool{"@FRM-01": false, "@NOTE-01": true, "": true} {
		s := &Scenario{tags: []string{"@M3", tags}}
		if err := s.frmGate(); errors.Is(err, godog.ErrPending) != pending {
			t.Errorf("%s：err = %v，預期 pending=%v", tags, err, pending)
		}
	}
}
