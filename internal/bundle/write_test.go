package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func outManifest() OutManifest {
	lang := "en"
	return OutManifest{
		Item: OutItem{Platform: "youtube", ExternalID: "abc", Title: "T", Availability: "public"},
		Artifacts: []OutArtifact{{ID: "0190a1b2-0000-7000-8000-0000000000a1", Kind: "transcript", Variant: "corrected", Lang: &lang,
			Format: CuesFormat, Path: "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json", Provenance: json.RawMessage(`{"executor":"api"}`)}},
		CreatedBy: OutCreatedBy{InstanceID: "0190a1b2-0000-7000-8000-000000000001", AppVersion: "0.4.0"},
		CreatedAt: "2026-11-23T10:00:00Z",
	}
}

func TestWriteRoundTrip(t *testing.T) {
	c := cues(t)
	img := jpegBytes(t)
	m := outManifest()
	entries := []OutEntry{
		{Path: "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json", Data: c},
		{Path: "artifacts/0190a1b2-0000-7000-8000-0000000000f1-001.jpg", Data: img},
	}
	data, err := Write(m, entries)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(data, Options{})
	if err != nil {
		t.Fatalf("Read：%v", err)
	}
	if err := b.ValidateContents(); err != nil {
		t.Fatalf("ValidateContents：%v", err)
	}
	if len(b.Manifest.Files) != 2 || b.Manifest.Artifacts[0].Parents != nil || b.Manifest.License != "" {
		t.Errorf("manifest = %+v", b.Manifest)
	}
	// manifest.json 是第一個項目，mode 0644、deflate。
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if zr.File[0].Name != ManifestName || zr.File[0].Mode().Perm() != 0o644 || zr.File[0].Method != zip.Deflate {
		t.Errorf("第一個項目 = %s %v %d", zr.File[0].Name, zr.File[0].Mode(), zr.File[0].Method)
	}
	// 同樣的內容產生同樣的 bytes（順序無關）。
	again, err := Write(m, []OutEntry{entries[1], entries[0]})
	if err != nil || !bytes.Equal(again, data) {
		t.Error("Write 不是確定性的")
	}
	if !strings.Contains(string(b.ManifestJSON), `"variant": "corrected"`) || strings.Contains(string(b.ManifestJSON), `"variant": ""`) {
		t.Errorf("variant 只在 transcript 寫出：%s", b.ManifestJSON)
	}
}

func TestWriteRejectsBadNames(t *testing.T) {
	for _, entries := range [][]OutEntry{
		{{Path: "manifest.json"}},
		{{Path: "artifacts/"}},
		{{Path: "../x"}},
		{{Path: "artifacts/a/b"}},
		{{Path: "artifacts/A.json"}, {Path: "artifacts/a.json"}},
	} {
		if _, err := Write(outManifest(), entries); err == nil {
			t.Errorf("Write(%v) 應該失敗", entries)
		}
	}
	if _, err := Write(OutManifest{}, nil); err != nil {
		t.Errorf("空的 bundle：%v", err)
	}
}

func TestValidLang(t *testing.T) {
	for s, want := range map[string]bool{"en": true, "zh-Hant": true, "zh-Hant-TW": true, "": false, "e": false, "en_US": false} {
		if ValidLang(s) != want {
			t.Errorf("ValidLang(%q) != %v", s, want)
		}
	}
}

// TestStreamFromFilesMatchesWrite：內容放在檔案中（File 項目，NFR-RES-3）時，串流寫出的 bytes 與 Write 相同；
// 檔案在 Prepare 之後被換掉時停止寫出。
func TestStreamFromFilesMatchesWrite(t *testing.T) {
	big := bytes.Repeat(cues(t), 200) // 大於 flate 的視窗與 io.Copy 的緩衝
	img := jpegBytes(t)
	m := outManifest()
	want, err := Write(m, []OutEntry{
		{Path: "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json", Data: big},
		{Path: "artifacts/0190a1b2-0000-7000-8000-0000000000f1-001.jpg", Data: img},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "a.json")
	if err := os.WriteFile(f, big, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(big)
	pl, err := Prepare(m, []OutEntry{
		{Path: "artifacts/0190a1b2-0000-7000-8000-0000000000f1-001.jpg", Data: img},
		{Path: "artifacts/0190a1b2-0000-7000-8000-0000000000a1.json", File: f, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(big))},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := pl.Stream(&got); err != nil || !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("串流寫出的 bytes 與 Write 不同（%v）", err)
	}
	if err := os.WriteFile(f, append(big, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pl.Stream(io.Discard); err == nil {
		t.Fatal("檔案改變了，仍然寫完 zip")
	}
}
