package groq

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// meterLog 記錄計費掛勾的呼叫。
type meterLog struct {
	before  int
	expectS float64
	bills   []Billing
	block   error
}

func (m *meterLog) opts(expectS float64) []Option {
	return []Option{WithExpectedDuration(expectS), WithMeter(Meter{
		Before: func(_ context.Context, s float64) error { m.before++; m.expectS = s; return m.block },
		After:  func(_ context.Context, b Billing) { m.bills = append(m.bills, b) },
	})}
}

// COST-R1、C16：送出之後依回應分類；沒有送出（沒有 key、音檔太大）時不呼叫掛勾。
func TestTranscribeBillingOutcomes(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want Billing
		err  bool
	}{
		{"成功時是回報的長度", 200, okBody, Billing{Outcome: OutcomeOK, DurationS: 12.5}, false},
		{"沒有語音仍計費", 200, `{"language":"english","duration":4,"segments":[]}`, Billing{Outcome: OutcomeOK, DurationS: 4}, true},
		{"沒有回報長度", 200, `{"language":"english","segments":[{"start":0,"end":1,"text":"hi"}]}`, Billing{Outcome: OutcomeOK}, false},
		{"狀態碼", 503, `{}`, Billing{Outcome: OutcomeHTTPError}, true},
		{"key 被拒", 401, `{}`, Billing{Outcome: OutcomeHTTPError}, true},
		{"不是 JSON", 200, `nope`, Billing{Outcome: OutcomeHTTPError}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := respond(tc.code, tc.body)
			defer srv.Close()
			var m meterLog
			_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "en", m.opts(30)...)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v", err)
			}
			if m.before != 1 || m.expectS != 30 || len(m.bills) != 1 || m.bills[0] != tc.want {
				t.Errorf("before = %d（%v），bills = %+v，預期 %+v", m.before, m.expectS, m.bills, tc.want)
			}
		})
	}
}

func TestTranscribeBillingNetworkAndNotSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	var m meterLog
	c := &Client{HTTP: http.DefaultClient, BaseURL: url, APIKey: testKey}
	if _, err := c.Transcribe(context.Background(), audioFile(t), "en", m.opts(1)...); err == nil {
		t.Fatal("預期連線錯誤")
	}
	if len(m.bills) != 1 || m.bills[0].Outcome != OutcomeNetworkError {
		t.Errorf("bills = %+v", m.bills)
	}

	// 沒有 key：不送出，也不預留。
	var none meterLog
	c = &Client{HTTP: http.DefaultClient, BaseURL: url}
	if _, err := c.Transcribe(context.Background(), audioFile(t), "en", none.opts(1)...); !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v", err)
	}
	if none.before != 0 || len(none.bills) != 0 {
		t.Errorf("沒有送出的請求不該呼叫掛勾：%+v", none)
	}
}

// Before 回傳錯誤（例如超過預算）時不送出請求，也不呼叫 After。
func TestTranscribeBlockedByTheMeterSendsNothing(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	blocked := errors.New("over budget")
	m := meterLog{block: blocked}
	if _, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "en", m.opts(1)...); !errors.Is(err, blocked) {
		t.Fatalf("err = %v", err)
	}
	if hits != 0 || len(m.bills) != 0 {
		t.Errorf("hits = %d，bills = %+v", hits, m.bills)
	}
	if got, s := MeterOf(m.opts(7)...); got.Before == nil || s != 7 {
		t.Error("MeterOf 要取回掛勾與估算長度")
	}
}
