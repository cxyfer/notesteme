package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// synchronousIn 讀交易所在連線的 PRAGMA synchronous（1 = NORMAL，2 = FULL）。
func (r *Repo) synchronousIn(ctx context.Context) (int, error) {
	var v int
	err := r.tx.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&v)
	return v, err
}

// TestTxDurable：TxDurable 的交易以 synchronous=FULL commit，結束後寫入連線恢復 NORMAL；
// 一般的 Tx 是 NORMAL。失敗時照樣回復，並行的 Tx 與 TxDurable 都不會卡住或互相改到設定（NFR-REL-3）。
func TestTxDurable(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	check := func(name string, tx func(context.Context, func(*Repo) error) error, want int) {
		t.Helper()
		var got int
		if err := tx(ctx, func(r *Repo) error {
			var err error
			got, err = r.synchronousIn(ctx)
			return err
		}); err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		if got != want {
			t.Errorf("%s 的 synchronous = %d，應該是 %d", name, got, want)
		}
	}
	check("Tx", s.Tx, 1)
	check("TxDurable", s.TxDurable, 2)
	check("TxDurable 之後的 Tx", s.Tx, 1)

	boom := errors.New("boom")
	if err := s.TxDurable(ctx, func(r *Repo) error {
		if err := r.SetSetting(ctx, "durable", "x"); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("TxDurable 應該回傳 fn 的錯誤：%v", err)
	}
	if _, ok, err := s.GetSetting(ctx, "durable"); err != nil || ok {
		t.Fatalf("失敗的 TxDurable 應該回復：%v, %v", ok, err)
	}
	check("失敗的 TxDurable 之後的 Tx", s.Tx, 1)

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, want := s.Tx, 1
			if i%2 == 0 {
				tx, want = s.TxDurable, 2
			}
			errs <- tx(ctx, func(r *Repo) error {
				if got, err := r.synchronousIn(ctx); err != nil || got != want {
					return errors.Join(err, errors.New("並行時 synchronous 被別的交易改掉"))
				}
				return r.SetSetting(ctx, "k", "v")
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	check("並行之後的 Tx", s.Tx, 1)
}
