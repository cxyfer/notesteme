package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// ErrArtifactNotFound 表示產出不存在（LIB-R25）：從來沒有、已被清除（PIPE-R7），或所屬影音已永久刪除。
var ErrArtifactNotFound = errors.New("找不到這個產出。")

// ArtifactVersion 是產出保留中的一個版本（LIB-R25）；Current 表示它是那一組的現行版本（沒有被取代）。
type ArtifactVersion struct {
	store.Artifact
	Current bool
}

// ItemArtifacts 列出影音每個產出保留中的每個版本（LIB-R25，LIB-R11 的「可比較」）：
// 依 (kind, variant, lang) 分組，組的順序是現行版本的建立順序，組內由新到舊。
// 影音不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) ItemArtifacts(ctx context.Context, itemID string) ([]ArtifactVersion, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, &InTrashError{ItemID: it.ID}
	}
	cur, err := r.CurrentArtifacts(ctx, itemID, "")
	if err != nil {
		return nil, err
	}
	type group struct{ kind, variant, lang string }
	seen := map[group]bool{}
	out := []ArtifactVersion{}
	for _, a := range cur {
		g := group{a.Kind, a.Variant, a.Lang}
		if seen[g] {
			continue
		}
		seen[g] = true
		vs, err := r.ArtifactGroupVersions(ctx, itemID, g.kind, g.variant, g.lang)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			out = append(out, ArtifactVersion{Artifact: v, Current: v.SupersededBy == ""})
		}
	}
	return out, nil
}

// ArtifactWithItem 以 ID 取得產出與所屬影音（LIB-R25）。產出不存在時回傳 ErrArtifactNotFound；
// 影音在垃圾桶中時回傳 *InTrashError（DEL-R1：垃圾桶中的影音不能讀取內容）。
func (p *Pipeline) ArtifactWithItem(ctx context.Context, artifactID string) (store.Item, store.Artifact, error) {
	r := p.st.Read()
	a, err := r.Artifact(ctx, artifactID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Item{}, store.Artifact{}, ErrArtifactNotFound
	}
	if err != nil {
		return store.Item{}, store.Artifact{}, err
	}
	it, err := r.ItemByID(ctx, a.ItemID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Item{}, store.Artifact{}, ErrArtifactNotFound
	}
	if err != nil {
		return store.Item{}, store.Artifact{}, err
	}
	if it.DeletedAt != nil {
		return store.Item{}, store.Artifact{}, &InTrashError{ItemID: it.ID}
	}
	return it, a, nil
}

// TranscriptVersion 讀取一個字幕版本（kind 必須是 transcript）。sha256 不符或檔案不見時交給 handleCorrupt：
// 標記損毀；只有現行版本會從產生它的步驟重新處理（NFR-REL-2），舊版本只標記，回傳 *CorruptArtifactError。
func (p *Pipeline) TranscriptVersion(ctx context.Context, a store.Artifact) (subtitle.Transcript, error) {
	return p.readTranscriptArtifact(ctx, a)
}

// NoteVersion 讀取一個筆記版本（kind 必須是 note）的 Markdown，損毀的處理同 TranscriptVersion。
func (p *Pipeline) NoteVersion(ctx context.Context, a store.Artifact) (string, error) {
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(a.Path)))
	if errors.Is(err, os.ErrNotExist) || (err == nil && sha256Hex(b) != a.SHA256) {
		return "", p.handleCorrupt(ctx, a)
	}
	if err != nil {
		return "", err
	}
	var doc noteDoc
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&doc); err != nil {
		return "", err
	}
	return doc.Markdown, nil
}
