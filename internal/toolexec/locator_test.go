package toolexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 建立受管理的 venv：<managed>/yt-dlp/<id>/bin/yt-dlp，yt-dlp-venv 指向它。
func writeManaged(t *testing.T, managed, id string) string {
	t.Helper()
	bin := filepath.Join(managed, "yt-dlp", id, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, bin, "yt-dlp", 0o755)
	link := filepath.Join(managed, "yt-dlp-venv")
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join("yt-dlp", id), link); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(link, "bin", "yt-dlp")
}

// 順序：設定檔 → 環境變數 → 受管理的 venv → PATH，每一層都回報自己的來源。
func TestLocateOrderAndSources(t *testing.T) {
	pathDir, envDir, cfgDir, managed := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	viaPath := writeTool(t, pathDir, "yt-dlp", 0o755)
	viaEnv := writeTool(t, envDir, "yt-dlp", 0o755)
	viaCfg := writeTool(t, cfgDir, "yt-dlp", 0o755)
	viaManaged := writeManaged(t, managed, "2026.11.01")

	r := Resolver{Vars: map[string]string{"PATH": pathDir}, ManagedDir: managed}
	steps := []struct {
		name   string
		mutate func()
		path   string
		source Source
	}{
		{"受管理的 venv 優先於 PATH", func() {}, viaManaged, SourceManaged},
		{"環境變數優先於受管理的 venv", func() { r.Vars["NOTESTEME_YTDLP"] = viaEnv }, viaEnv, SourceEnv},
		{"設定檔優先於環境變數", func() { r.Config = map[string]string{"yt-dlp": viaCfg} }, viaCfg, SourceConfigFile},
	}
	for _, st := range steps {
		st.mutate()
		loc := r.Locate(YtDlp)
		if loc.Path != st.path || loc.Source != st.source || !loc.Available() {
			t.Errorf("%s：%+v，預期 %s（%s）", st.name, loc, st.path, st.source)
		}
	}

	// 沒有受管理的 venv 時用 PATH。
	r = Resolver{Vars: map[string]string{"PATH": pathDir}, ManagedDir: t.TempDir()}
	if loc := r.Locate(YtDlp); loc.Path != viaPath || loc.Source != SourcePath || !loc.Available() {
		t.Errorf("PATH：%+v", loc)
	}
	// 沒有指定 ManagedDir 就不看受管理的 venv。
	r = Resolver{Vars: map[string]string{"PATH": pathDir}}
	if loc := r.Locate(YtDlp); loc.Source != SourcePath {
		t.Errorf("沒有 ManagedDir：%+v", loc)
	}
}

// 只有 yt-dlp 有受管理的 venv：其他工具即使目錄下有同名 symlink 也不看。
func TestLocateManagedVenvIsOnlyForManagedTools(t *testing.T) {
	managed := t.TempDir()
	writeManaged(t, managed, "2026.11.01")
	r := Resolver{Vars: map[string]string{"PATH": t.TempDir()}, ManagedDir: managed}
	if loc := r.Locate(FFmpeg); loc.Source != SourcePath || loc.Available() {
		t.Errorf("ffmpeg：%+v", loc)
	}
}

// 設定檔或環境變數指定了不能執行的路徑：仍然回報該路徑與來源，available 為 false，不退回後面的來源。
func TestLocateReportsBrokenPathsWithTheirSource(t *testing.T) {
	pathDir := t.TempDir()
	writeTool(t, pathDir, "ffmpeg", 0o755)

	r := Resolver{Vars: map[string]string{"PATH": pathDir, "NOTESTEME_FFMPEG": "/usr/local/bin/ffmpeg-gone"},
		Config: map[string]string{"ffmpeg": "/opt/ffmpeg/bin/ffmpeg"}}
	loc := r.Locate(FFmpeg)
	if loc.Path != "/opt/ffmpeg/bin/ffmpeg" || loc.Source != SourceConfigFile || loc.Available() {
		t.Fatalf("設定檔：%+v", loc)
	}
	if !strings.Contains(loc.Err.Error(), "tools.ffmpeg") {
		t.Errorf("錯誤應指出是設定檔的哪個欄位：%v", loc.Err)
	}

	r.Config = nil
	loc = r.Locate(FFmpeg)
	if loc.Path != "/usr/local/bin/ffmpeg-gone" || loc.Source != SourceEnv || loc.Available() {
		t.Fatalf("環境變數：%+v", loc)
	}
	if !strings.Contains(loc.Err.Error(), "NOTESTEME_FFMPEG") {
		t.Errorf("錯誤應指出是哪個環境變數：%v", loc.Err)
	}
}

// 受管理的 venv 壞了（symlink 在、目標被刪掉）：回報錯誤，不悄悄改用 PATH 上的版本；Resolve 同樣失敗。
func TestLocateBrokenManagedVenvDoesNotFallBackToPath(t *testing.T) {
	pathDir, managed := t.TempDir(), t.TempDir()
	writeTool(t, pathDir, "yt-dlp", 0o755)
	writeManaged(t, managed, "2026.11.01")
	if err := os.RemoveAll(filepath.Join(managed, "yt-dlp", "2026.11.01")); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Vars: map[string]string{"PATH": pathDir}, ManagedDir: managed}
	loc := r.Locate(YtDlp)
	if loc.Source != SourceManaged || loc.Available() || loc.Path == "" {
		t.Fatalf("%+v", loc)
	}
	if _, err := r.Resolve(YtDlp); err == nil {
		t.Error("Resolve 應失敗")
	}
}

func TestLocateNotFoundInPath(t *testing.T) {
	loc := (Resolver{Vars: map[string]string{"PATH": t.TempDir()}}).Locate(YtDlp)
	if loc.Source != SourcePath || loc.Path != "" || loc.Available() {
		t.Errorf("%+v", loc)
	}
}
