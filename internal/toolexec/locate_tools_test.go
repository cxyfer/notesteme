package toolexec

import "testing"

func TestFFprobeAndDenoSpecs(t *testing.T) {
	dir, envDir := t.TempDir(), t.TempDir()
	probe := writeTool(t, dir, "ffprobe", 0o755)
	deno := writeTool(t, dir, "deno", 0o755)
	viaEnv := writeTool(t, envDir, "deno", 0o755)

	r := Resolver{Vars: map[string]string{"PATH": dir}}
	if p, err := r.Resolve(FFprobe); err != nil || p != probe {
		t.Errorf("ffprobe：%q, %v", p, err)
	}
	if p, err := r.Resolve(Deno); err != nil || p != deno {
		t.Errorf("deno：%q, %v", p, err)
	}
	r.Vars["NOTESTEME_DENO"] = viaEnv
	if p, err := r.Resolve(Deno); err != nil || p != viaEnv {
		t.Errorf("NOTESTEME_DENO 應優先於 PATH：%q, %v", p, err)
	}
	r.Config = map[string]string{"ffprobe": "/nonexistent/ffprobe"}
	if _, err := r.Resolve(FFprobe); err == nil {
		t.Error("設定檔的 ffprobe 路徑無效時應回報錯誤")
	}
}
