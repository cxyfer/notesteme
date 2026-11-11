package toolexec

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTool(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLookPathReturnsAbsolutePathFromGivenPATH(t *testing.T) {
	dir := t.TempDir()
	tool := writeTool(t, dir, "yt-dlp", 0o755)
	writeTool(t, dir, "ffmpeg", 0o644) // 沒有執行權限

	sep := string(os.PathListSeparator)
	if p, err := LookPath("yt-dlp", "/nonexistent"+sep+dir); err != nil || p != tool {
		t.Errorf("LookPath = %q, %v；預期 %q", p, err, tool)
	}
	if _, err := LookPath("ffmpeg", dir); err == nil {
		t.Error("沒有執行權限的檔案不算")
	}
	// 相對的 PATH 項目與空項目不採用，避免在目前目錄執行工具。
	if _, err := LookPath("yt-dlp", ""+sep+"."+sep+filepath.Base(dir)); err == nil {
		t.Error("相對路徑的 PATH 項目不應被採用")
	}
	if _, err := LookPath("../yt-dlp", dir); err == nil {
		t.Error("名稱含路徑時應拒絕")
	}
}

func TestResolveOrder(t *testing.T) {
	pathDir, envDir, cfgDir := t.TempDir(), t.TempDir(), t.TempDir()
	viaPath := writeTool(t, pathDir, "ffmpeg", 0o755)
	viaEnv := writeTool(t, envDir, "ffmpeg", 0o755)
	viaCfg := writeTool(t, cfgDir, "ffmpeg", 0o755)

	r := Resolver{Vars: map[string]string{"PATH": pathDir}}
	if p, err := r.Resolve(FFmpeg); err != nil || p != viaPath {
		t.Errorf("PATH：%q, %v", p, err)
	}
	r.Vars["NOTESTEME_FFMPEG"] = viaEnv
	if p, err := r.Resolve(FFmpeg); err != nil || p != viaEnv {
		t.Errorf("環境變數應優先於 PATH：%q, %v", p, err)
	}
	r.Config = map[string]string{"ffmpeg": viaCfg}
	if p, err := r.Resolve(FFmpeg); err != nil || p != viaCfg {
		t.Errorf("設定檔應優先於環境變數：%q, %v", p, err)
	}
	// 較高優先的來源指定了壞的路徑時，不退回後面的來源。
	r.Config = map[string]string{"ffmpeg": "/nonexistent/ffmpeg"}
	if _, err := r.Resolve(FFmpeg); err == nil {
		t.Error("設定檔的路徑無效時應回報錯誤")
	}
	// 沒有程序的 PATH 就找不到。
	if _, err := (Resolver{}).Resolve(YtDlp); err == nil {
		t.Error("沒有任何來源時應回報錯誤")
	}
}
