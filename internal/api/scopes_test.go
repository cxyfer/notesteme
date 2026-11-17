package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
)

// TestScopeMatrix checks scope enforcement on every protected endpoint, including 501 wrappers
// and verifying that scope checks run before Pipeline == nil / Paths.Check / 501 checks.
func TestScopeMatrix(t *testing.T) {
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

	makeTok := func(name string, scopes ...string) string {
		ct, err := svc.CreateToken(context.Background(), name, scopes, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ct.Token
	}
	toks := map[string]string{
		auth.ScopeRead:   makeTok("r", auth.ScopeRead),
		auth.ScopeAgent:  makeTok("ag", auth.ScopeAgent),
		auth.ScopeIngest: makeTok("in", auth.ScopeIngest),
		auth.ScopeAdmin:  makeTok("ad", auth.ScopeAdmin),
		"all":            makeTok("all", auth.ScopeRead, auth.ScopeAgent, auth.ScopeIngest, auth.ScopeAdmin),
	}

	eventsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for name, events := range map[string]http.Handler{"events_nil": nil, "events_live": eventsHandler} {
		t.Run(name, func(t *testing.T) {
			h := New(Deps{
				Store:  st,
				Auth:   svc,
				Clock:  clk,
				Log:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
				Paths:  pathguard.New([]string{t.TempDir()}), // deny outside paths
				Events: events,
			})

			cases := []struct {
				method, path, body string
				wantScope          string
				wrongScope         string
				wantAdminStatus    int // expected status when called with right scope (with nil Pipeline/etc.)
			}{
				// read
				{"GET", "/api/items", "", auth.ScopeRead, auth.ScopeAdmin, 200},
				{"GET", "/api/items/x", "", auth.ScopeRead, auth.ScopeAdmin, 404},
				{"GET", "/api/items/x/cues", "", auth.ScopeRead, auth.ScopeAdmin, 404},
				{"GET", "/api/items/x/subtitles", "", auth.ScopeRead, auth.ScopeAdmin, 404},
				{"GET", "/api/items/x/runs", "", auth.ScopeRead, auth.ScopeAdmin, 404},
				{"GET", "/api/items/x/deletion-preview", "", auth.ScopeRead, auth.ScopeAdmin, 500},
				{"GET", "/api/trash", "", auth.ScopeRead, auth.ScopeAdmin, 500},
				{"GET", "/api/queue", "", auth.ScopeRead, auth.ScopeAdmin, 501},
				{"GET", "/api/providers/p1", "", auth.ScopeRead, auth.ScopeAdmin, 501},
				// ingest
				{"POST", "/api/items", `{"path":"/etc/passwd"}`, auth.ScopeIngest, auth.ScopeRead, 403},
				{"POST", "/api/resolve", `{"url":"https://example.com"}`, auth.ScopeIngest, auth.ScopeRead, 500},
				{"POST", "/api/items/x/rerun", `{"from":"normalize"}`, auth.ScopeIngest, auth.ScopeRead, 500},
				{"POST", "/api/items/x/cancel", "", auth.ScopeIngest, auth.ScopeRead, 500},
				{"POST", "/api/estimate", "", auth.ScopeIngest, auth.ScopeRead, 501},
				// admin
				{"GET", "/api/settings/services", "", auth.ScopeAdmin, auth.ScopeRead, 501},
				{"PUT", "/api/settings/services", "", auth.ScopeAdmin, auth.ScopeIngest, 501},
				{"POST", "/api/providers", `{"type":"command"}`, auth.ScopeAdmin, auth.ScopeRead, 501},
				{"PATCH", "/api/queue/limits", `{"download":1}`, auth.ScopeAdmin, auth.ScopeIngest, 501},
				{"POST", "/api/queue/pause", "", auth.ScopeAdmin, auth.ScopeAgent, 501},
				{"POST", "/api/queue/resume", "", auth.ScopeAdmin, auth.ScopeRead, 501},
				{"DELETE", "/api/items/x", "", auth.ScopeAdmin, auth.ScopeIngest, 500},
				{"POST", "/api/trash/x/restore", "", auth.ScopeAdmin, auth.ScopeIngest, 500},
				{"DELETE", "/api/trash/x", "", auth.ScopeAdmin, auth.ScopeIngest, 500},
				{"DELETE", "/api/trash", "", auth.ScopeAdmin, auth.ScopeRead, 500},
				{"GET", "/api/tokens", "", auth.ScopeAdmin, auth.ScopeRead, 200},
				{"POST", "/api/tokens", `{"name":"t","scopes":["read"]}`, auth.ScopeAdmin, auth.ScopeRead, 201},
				{"DELETE", "/api/tokens/missing", "", auth.ScopeAdmin, auth.ScopeRead, 404},
				{"POST", "/api/tasks/t1/retry", "", auth.ScopeAdmin, auth.ScopeAgent, 501},
				{"POST", "/api/tasks/t1/cancel", "", auth.ScopeAdmin, auth.ScopeAgent, 501},
			}

			for _, c := range cases {
				// Wrong scope -> 403 with required_scope naming the missing scope.
				req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				if c.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				req.Header.Set("Authorization", "Bearer "+toks[c.wrongScope])
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				e := decodeError(t, rec.Body.Bytes())
				if rec.Code != http.StatusForbidden || e.Code != CodeForbidden || e.RequiredScope != c.wantScope || !strings.Contains(e.Message, c.wantScope) {
					t.Errorf("%s %s with %q: status=%d body=%s (want 403 required_scope=%s)", c.method, c.path, c.wrongScope, rec.Code, rec.Body, c.wantScope)
				}

				// Right scope passes authz (reaches handler / 501 / expected status).
				if c.path == "/api/tokens" && c.method == "POST" {
					// avoid duplicate token name across subtests
					c.body = `{"name":"t-` + name + `","scopes":["read"]}`
				}
				req2 := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				if c.body != "" {
					req2.Header.Set("Content-Type", "application/json")
				}
				req2.Header.Set("Authorization", "Bearer "+toks[c.wantScope])
				rec2 := httptest.NewRecorder()
				h.ServeHTTP(rec2, req2)
				if rec2.Code != c.wantAdminStatus {
					t.Errorf("%s %s with %q: status=%d body=%s, want %d", c.method, c.path, c.wantScope, rec2.Code, rec2.Body, c.wantAdminStatus)
				}
			}

			// GET /api/events: admin token lacks read -> 403 required_scope=read; read token -> 200 or 501.
			req := httptest.NewRequest("GET", "/api/events", nil)
			req.Header.Set("Authorization", "Bearer "+toks[auth.ScopeAdmin])
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			e := decodeError(t, rec.Body.Bytes())
			if rec.Code != 403 || e.RequiredScope != auth.ScopeRead {
				t.Errorf("GET /api/events with admin: %d %s", rec.Code, rec.Body)
			}

			req2 := httptest.NewRequest("GET", "/api/events", nil)
			req2.Header.Set("Authorization", "Bearer "+toks[auth.ScopeRead])
			rec2 := httptest.NewRecorder()
			h.ServeHTTP(rec2, req2)
			wantEv := 501
			if events != nil {
				wantEv = 200
			}
			if rec2.Code != wantEv {
				t.Errorf("GET /api/events with read: %d %s, want %d", rec2.Code, rec2.Body, wantEv)
			}

			// Owner-only endpoints refuse even a token carrying all scopes.
			ownerOnly := [][3]string{
				{"POST", "/api/auth/logout", ""},
				{"POST", "/api/cookies", `{"platform":"youtube","content":"a=1"}`},
				{"POST", "/api/backup", ""},
				{"GET", "/api/system", ""},
				{"POST", "/api/tools/yt-dlp/update", `{}`},
				{"POST", "/api/tools/yt-dlp/rollback", ""},
			}
			for _, c := range ownerOnly {
				req := httptest.NewRequest(c[0], c[1], strings.NewReader(c[2]))
				if c[2] != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				req.Header.Set("Authorization", "Bearer "+toks["all"])
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != 403 || decodeError(t, rec.Body.Bytes()).Code != CodeForbidden {
					t.Errorf("owner-only %s %s with all-scopes token: %d %s", c[0], c[1], rec.Code, rec.Body)
				}
			}
		})
	}
}

func TestDELR12ScopeWithPipeline(t *testing.T) {
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) { svc = d.Auth })
	id := r.ingestOne(abcURL)

	readTok, err := svc.CreateToken(context.Background(), "reader", []string{auth.ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nonAdminTok, err := svc.CreateToken(context.Background(), "non-admin", []string{auth.ScopeRead, auth.ScopeAgent, auth.ScopeIngest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := svc.CreateToken(context.Background(), "adm", []string{auth.ScopeAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// read token can view trash & deletion-preview.
	if rec := r.do("GET", "/api/trash", "", withBearer(readTok.Token)); rec.Code != 200 {
		t.Errorf("read GET /api/trash = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("GET", "/api/items/"+id+"/deletion-preview", "", withBearer(readTok.Token)); rec.Code != 200 {
		t.Errorf("read deletion-preview = %d %s", rec.Code, rec.Body)
	}

	// non-admin token cannot delete/restore/purge.
	if rec := r.do("DELETE", "/api/items/"+id, "", withBearer(nonAdminTok.Token)); rec.Code != 403 || decodeError(t, rec.Body.Bytes()).RequiredScope != auth.ScopeAdmin {
		t.Errorf("non-admin DELETE item = %d %s", rec.Code, rec.Body)
	}

	// admin token can delete, restore, delete again, and empty trash.
	if rec := r.do("DELETE", "/api/items/"+id, "", withBearer(adminTok.Token)); rec.Code != 200 {
		t.Fatalf("admin DELETE item = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("POST", "/api/trash/"+id+"/restore", "", withBearer(adminTok.Token)); rec.Code != 200 {
		t.Fatalf("admin restore = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", "/api/items/"+id, "", withBearer(adminTok.Token)); rec.Code != 200 {
		t.Fatalf("admin DELETE item 2 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", "/api/trash", "", withBearer(nonAdminTok.Token)); rec.Code != 403 {
		t.Errorf("non-admin empty trash = %d %s", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", "/api/trash", "", withBearer(adminTok.Token)); rec.Code != 200 {
		t.Errorf("admin empty trash = %d %s", rec.Code, rec.Body)
	}
}
