package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 工具版本的快取（OPS-R22、NFR-OBS-3）：GET /api/system 回報每個外部工具的版本，但不能因為執行外部指令而變慢。
//   - 以「解開 symlink 後的實際路徑 + 大小 + 修改時間」當作快取鍵：受管理的 yt-dlp 是 symlink（yt-dlp-venv），
//     更新或回退後路徑字串不變、指向的檔案改變，快取鍵跟著變，不會一直回報舊版本。
//   - 沒有快取時，所有工具同時執行一次（共用一個 versionProbeTimeout 的期限），不會逐一等待。
//   - 失敗或逾時也快取一段時間（versionFailTTL），工具壞掉時不會每次都卡住；成功的結果在快取鍵不變時保留 versionOKTTL。
const (
	versionProbeTimeout = 2 * time.Second
	versionOKTTL        = 10 * time.Minute
	versionFailTTL      = time.Minute
)

// versionArgs 是各工具回報版本的參數。
var versionArgs = map[string][]string{
	toolexec.YtDlp.Name:   {"--version"},
	toolexec.FFmpeg.Name:  {"-version"},
	toolexec.FFprobe.Name: {"-version"},
	toolexec.Deno.Name:    {"--version"},
}

type versionEntry struct {
	key     string
	version string
	until   time.Time
}

// toolVersions 是工具版本的快取；每個 Server 一個（toolVersionCache）。
type toolVersions struct {
	runner  toolexec.Runner
	now     func() time.Time
	timeout time.Duration

	mu      sync.Mutex
	entries map[string]versionEntry
}

func newToolVersions(runner toolexec.Runner, now func() time.Time) *toolVersions {
	return &toolVersions{runner: runner, now: now, timeout: versionProbeTimeout, entries: map[string]versionEntry{}}
}

// toolVersionCache 回傳這個 Server 的工具版本快取（第一次呼叫時建立）。
func (s *Server) toolVersionCache(env Env) *toolVersions {
	s.versionsOnce.Do(func() {
		runner := s.runner
		if runner == nil {
			runner = env.Runner
		}
		if runner == nil {
			runner = toolexec.New(env.Vars)
		}
		now := time.Now
		if env.Clock != nil {
			now = env.Clock.Now
		}
		s.versions = newToolVersions(runner, now)
	})
	return s.versions
}

// cacheKey 是工具檔案的識別：解開 symlink 後的路徑、大小與修改時間。讀不到時回傳空字串（不快取、不執行）。
func cacheKey(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	fi, err := os.Stat(real)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", real, fi.Size(), fi.ModTime().UnixNano())
}

// Versions 回傳 locs 中每個可以執行的工具的版本（key 是工具名稱）；不知道版本的工具不在結果中。
func (v *toolVersions) Versions(ctx context.Context, locs map[string]toolexec.Location) map[string]string {
	out := map[string]string{}
	keys := map[string]string{}
	var miss []string
	now := v.now()
	v.mu.Lock()
	for name, loc := range locs {
		if !loc.Available() || loc.Path == "" || versionArgs[name] == nil {
			continue
		}
		k := cacheKey(loc.Path)
		if k == "" {
			continue
		}
		keys[name] = k
		if e, ok := v.entries[name]; ok && e.key == k && now.Before(e.until) {
			if e.version != "" {
				out[name] = e.version
			}
			continue
		}
		miss = append(miss, name)
	}
	v.mu.Unlock()
	if len(miss) == 0 {
		return out
	}

	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	type result struct{ name, version string }
	ch := make(chan result, len(miss))
	for _, name := range miss {
		go func(name string) {
			ch <- result{name, v.probe(ctx, name, locs[name].Path)}
		}(name)
	}
	got := make(map[string]string, len(miss))
	for range miss {
		r := <-ch
		got[r.name] = r.version
	}

	now = v.now()
	v.mu.Lock()
	defer v.mu.Unlock()
	for name, ver := range got {
		ttl := versionOKTTL
		if ver == "" {
			ttl = versionFailTTL
		}
		v.entries[name] = versionEntry{key: keys[name], version: ver, until: now.Add(ttl)}
		if ver != "" {
			out[name] = ver
		}
	}
	return out
}

// probe 執行一次工具的版本指令；失敗、逾時或認不出版本時回傳空字串。
func (v *toolVersions) probe(ctx context.Context, name, path string) string {
	res, err := v.runner.Run(ctx, toolexec.Cmd{Path: path, Args: versionArgs[name], Timeout: v.timeout})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return parseToolVersion(name, string(res.Stdout))
}

// maxVersionLen 是回報的版本字串的上限：版本來自外部程式的輸出，太長的視為認不出來。
const maxVersionLen = 64

// parseToolVersion 從版本指令的輸出取出版本號：
//   - yt-dlp：第一行就是版本（2026.11.01）。
//   - ffmpeg／ffprobe：「ffmpeg version 7.1 Copyright …」取 version 後的那個字。
//   - deno：「deno 2.1.4 (stable, …)」取工具名稱後的那個字。
func parseToolVersion(name, out string) string {
	line := firstNonEmptyLine(out)
	f := strings.Fields(line)
	var v string
	switch {
	case len(f) == 0:
	case len(f) >= 3 && f[1] == "version":
		v = f[2]
	case len(f) >= 2 && f[0] == name:
		v = f[1]
	case len(f) == 1:
		v = f[0]
	}
	if len(v) > maxVersionLen || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	return v
}

func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
