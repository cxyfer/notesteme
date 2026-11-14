package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

type fakeBackup struct {
	name     string
	body     string
	writeErr error
	closed   bool
}

func (f *fakeBackup) Name() string { return f.name }
func (f *fakeBackup) WriteTo(w io.Writer) (int64, error) {
	n, _ := io.WriteString(w, f.body)
	return int64(n), f.writeErr
}
func (f *fakeBackup) Close() error { f.closed = true; return nil }

type fakeBackupSource struct {
	b        *fakeBackup
	beginErr error
}

func (f *fakeBackupSource) Begin(context.Context) (Backup, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.b, nil
}

func newBackupRig(t *testing.T, src BackupSource) (*testServer, http.Handler) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	d := Deps{Store: st, Auth: svc, Clock: clk, Version: "test", Backup: src,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	return &testServer{h: New(d), clk: clk}, NewLocal(d)
}

func TestBackupStreamsArchive(t *testing.T) {
	b := &fakeBackup{name: "notesteme-backup-20261112T100000Z.tar.gz", body: "tar-gz-bytes"}
	s, _ := newBackupRig(t, &fakeBackupSource{b: b})
	sess, csrf := s.login(t)
	rec := s.do("POST", "/api/backup", "", withCookies(sess, csrf, csrf))
	if rec.Code != 200 || rec.Body.String() != "tar-gz-bytes" {
		t.Fatalf("回應 = %d %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/gzip" {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
	if h.Get("Content-Disposition") != `attachment; filename="notesteme-backup-20261112T100000Z.tar.gz"` {
		t.Errorf("Content-Disposition = %q", h.Get("Content-Disposition"))
	}
	if h.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", h.Get("Cache-Control"))
	}
	if !b.closed {
		t.Error("備份用完要 Close（刪除暫存的快照）")
	}
}

// 本機（standalone 與 socket）不需要 session 與 CSRF。
func TestBackupLocal(t *testing.T) {
	b := &fakeBackup{name: "notesteme-backup-20261112T100000Z.tar.gz", body: "x"}
	_, local := newBackupRig(t, &fakeBackupSource{b: b})
	rec := httptest.NewRecorder()
	local.ServeHTTP(rec, httptest.NewRequest("POST", "/api/backup", nil))
	if rec.Code != 200 || rec.Body.String() != "x" {
		t.Fatalf("回應 = %d %q", rec.Code, rec.Body)
	}
}

func TestBackupRequiresLoginAndCSRF(t *testing.T) {
	b := &fakeBackup{name: "n", body: "secret-archive"}
	s, _ := newBackupRig(t, &fakeBackupSource{b: b})
	if rec := s.do("POST", "/api/backup", "", nil); rec.Code != 401 || strings.Contains(rec.Body.String(), "secret-archive") {
		t.Errorf("未登入 = %d %s", rec.Code, rec.Body)
	}
	sess, csrf := s.login(t)
	if rec := s.do("POST", "/api/backup", "", withCookies(sess, csrf, "")); rec.Code != 403 {
		t.Errorf("缺 CSRF = %d", rec.Code)
	}
	if b.closed {
		t.Error("沒有通過驗證的請求不該建立備份")
	}
}

// token 不能備份：備份含有整個資料庫。
func TestBackupRefusesToken(t *testing.T) {
	b := &fakeBackup{name: "n", body: "x"}
	rt := &routes{d: Deps{Backup: &fakeBackupSource{b: b}}}
	ctx := context.WithValue(t.Context(), keyPrincipal, auth.Principal{Kind: "token", Scopes: []string{"*"}})
	_, err := rt.createBackup(ctx, nil)
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusForbidden || e.Code != CodeForbidden {
		t.Fatalf("錯誤 = %v，預期 403 forbidden", err)
	}
}

// 快照失敗：回正常的 500 錯誤，不洩漏內部細節。
func TestBackupBeginFailure(t *testing.T) {
	s, _ := newBackupRig(t, &fakeBackupSource{beginErr: errors.New("disk full at /data/tmp/x")})
	sess, csrf := s.login(t)
	rec := s.do("POST", "/api/backup", "", withCookies(sess, csrf, csrf))
	if rec.Code != 500 || errCode(t, rec) != CodeInternal || strings.Contains(rec.Body.String(), "/data/tmp") {
		t.Errorf("回應 = %d %s", rec.Code, rec.Body)
	}
}

// 串流到一半失敗：中斷連線（ErrAbortHandler），不能讓 client 以為備份完整；備份仍然被清理。
func TestBackupWriteFailureAbortsConnection(t *testing.T) {
	b := &fakeBackup{name: "n", body: "partial", writeErr: errors.New("read failed")}
	_, local := newBackupRig(t, &fakeBackupSource{b: b})
	var got any
	func() {
		defer func() { got = recover() }()
		local.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/backup", nil))
	}()
	if got != http.ErrAbortHandler {
		t.Fatalf("recover = %v，預期 http.ErrAbortHandler", got)
	}
	if !b.closed {
		t.Error("失敗後也要清理備份")
	}
}
