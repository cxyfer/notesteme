package supadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type meterLog struct {
	before int
	bills  []Billing
	block  error
}

func (m *meterLog) opt() Option {
	return WithMeter(Meter{
		Before: func(context.Context) error { m.before++; return m.block },
		After:  func(_ context.Context, b Billing) { m.bills = append(m.bills, b) },
	})
}

// COST-R1、C16：依回應分類，credit 數讀 x-billable-requests（不合理的值當成沒有回報）。
func TestFetchBillingOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		header string
		body   string
		want   Billing
	}{
		{"成功", 200, "1", okBody, Billing{Outcome: OutcomeOK, Credits: 1, Reported: true}},
		{"成功但沒有回報", 200, "", okBody, Billing{Outcome: OutcomeOK}},
		{"206 沒有字幕仍計費", 206, "1", `{}`, Billing{Outcome: OutcomeOK, Credits: 1, Reported: true}},
		{"非同步任務", 202, "", `{"jobId":"j"}`, Billing{Outcome: OutcomeOK}},
		{"404", 404, "", `{}`, Billing{Outcome: OutcomeHTTPError}},
		{"429", 429, "0", `{}`, Billing{Outcome: OutcomeHTTPError, Reported: true}},
		{"不是 JSON", 200, "1", `nope`, Billing{Outcome: OutcomeHTTPError, Credits: 1, Reported: true}},
		{"負數", 200, "-3", okBody, Billing{Outcome: OutcomeOK}},
		{"不是整數", 200, "1.5", okBody, Billing{Outcome: OutcomeOK}},
		{"太大", 200, "99999999", okBody, Billing{Outcome: OutcomeOK}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set(HeaderBillable, tc.header)
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			var m meterLog
			c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, APIKey: "k"}
			_, _ = c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeAuto, m.opt())
			if m.before != 1 || len(m.bills) != 1 || m.bills[0] != tc.want {
				t.Errorf("before = %d，bills = %+v，預期 %+v", m.before, m.bills, tc.want)
			}
		})
	}
}

func TestFetchBillingNetworkBlockedAndNotSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	var m meterLog
	c := &Client{HTTP: http.DefaultClient, BaseURL: url, APIKey: "k"}
	if _, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeAuto, m.opt()); err == nil {
		t.Fatal("預期連線錯誤")
	}
	if len(m.bills) != 1 || m.bills[0].Outcome != OutcomeNetworkError {
		t.Errorf("bills = %+v", m.bills)
	}

	// 沒有 key：不送出，也不預留。
	var none meterLog
	c = &Client{HTTP: http.DefaultClient, BaseURL: url}
	if _, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeAuto, none.opt()); !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v", err)
	}
	if none.before != 0 || len(none.bills) != 0 {
		t.Errorf("none = %+v", none)
	}

	// Before 擋下時不送出。
	hits := 0
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer live.Close()
	blocked := errors.New("over budget")
	b := meterLog{block: blocked}
	c = &Client{HTTP: live.Client(), BaseURL: live.URL, APIKey: "k"}
	if _, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeAuto, b.opt()); !errors.Is(err, blocked) || hits != 0 || len(b.bills) != 0 {
		t.Errorf("err = %v，hits = %d，bills = %+v", err, hits, b.bills)
	}
	if MeterOf(b.opt()).Before == nil {
		t.Error("MeterOf 要取回掛勾")
	}
}

func TestEstimateCredits(t *testing.T) {
	for _, tc := range []struct {
		mode string
		dur  float64
		want float64
	}{
		{ModeNative, 3600, 1}, {ModeAuto, 0, 1}, {ModeGenerate, 30, 2}, {ModeGenerate, 61, 4}, {ModeAuto, 600, 20},
	} {
		if got := EstimateCredits(tc.mode, tc.dur); got != tc.want {
			t.Errorf("EstimateCredits(%s, %v) = %v，預期 %v", tc.mode, tc.dur, got, tc.want)
		}
	}
}
