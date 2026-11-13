package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAppEnvCopiesEnvAndInjectsWorldDeps(t *testing.T) {
	w := NewT(t)
	env := w.AppEnv()
	if env.Vars["NOTESTEME_PASSWORD"] != TestPassword || env.Vars["NOTESTEME_DATA"] != w.DataDir() {
		t.Errorf("vars = %v", env.Vars)
	}
	if env.Clock != w.Clock || env.Log == nil || env.Listen == nil || env.Test == nil || !env.Test.LowCostHashing {
		t.Errorf("env = %+v", env)
	}
	// 之後修改 World.Env 不影響已經組好的 Env。
	w.Env["NOTESTEME_PASSWORD"] = "changed"
	if env.Vars["NOTESTEME_PASSWORD"] != TestPassword {
		t.Error("AppEnv should copy the vars")
	}
}

func TestListenBindsLoopbackAndRecords(t *testing.T) {
	w := NewT(t)
	ln, err := w.Listen("tcp", "0.0.0.0:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := w.Listens()
	if len(got) != 1 || got[0].Configured != "0.0.0.0:8080" || got[0].Actual != ln.Addr().String() {
		t.Fatalf("listens = %+v", got)
	}
	if !strings.HasPrefix(ln.Addr().String(), "127.0.0.1:") {
		t.Errorf("addr = %s, want a 127.0.0.1 port", ln.Addr())
	}
}

// "unix" 照原樣 listen，而且不記進 Listens（那是 TCP 位址的對照）。
func TestListenUnixAsIs(t *testing.T) {
	w := NewT(t)
	path := filepath.Join(w.Root, "t.sock")
	ln, err := w.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln.Addr().Network() != "unix" || ln.Addr().String() != path {
		t.Errorf("addr = %s %s", ln.Addr().Network(), ln.Addr())
	}
	if got := w.Listens(); len(got) != 0 {
		t.Errorf("listens = %+v，unix 不應該被記錄", got)
	}
}

func TestLogBuffer(t *testing.T) {
	var l LogBuffer
	_, _ = l.Write([]byte("a"))
	_, _ = l.Write([]byte("b"))
	if l.String() != "ab" {
		t.Errorf("log = %q", l.String())
	}
}
