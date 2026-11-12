package app

import (
	"context"
	"testing"
)

// http.Server 必須設定讀取與閒置逾時；字幕下載是串流回應，所以不設 WriteTimeout。
func TestHTTPServerTimeouts(t *testing.T) {
	srv, err := StartServer(context.Background(), testEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	if srv.http.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout 未設定")
	}
	if srv.http.ReadTimeout <= 0 {
		t.Error("ReadTimeout 未設定")
	}
	if srv.http.IdleTimeout <= 0 {
		t.Error("IdleTimeout 未設定")
	}
	if srv.http.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v，串流回應不可設定", srv.http.WriteTimeout)
	}
}
