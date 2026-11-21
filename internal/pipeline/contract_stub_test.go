package pipeline

import (
	"context"
	"errors"
	"testing"
)

// M2 S1/S2 契約（Wave 0）：處理方案、頻道覆寫與 StageConfig 都是 stub，回傳 *NotImplementedError（API 層對應成 501）。
// WP-2 實作這些方法時刪除這個檔案，換成真正的測試。
func TestContractStubsAreNotImplemented(t *testing.T) {
	ctx := context.Background()
	p := &Pipeline{}
	checks := map[string]error{}
	_, err := p.Profiles(ctx)
	checks["Profiles"] = err
	_, err = p.Profile(ctx, "x")
	checks["Profile"] = err
	_, err = p.CreateProfile(ctx, "x", nil)
	checks["CreateProfile"] = err
	_, err = p.PatchProfile(ctx, "x", nil)
	checks["PatchProfile"] = err
	checks["DeleteProfile"] = p.DeleteProfile(ctx, "x")
	_, err = p.ProfileVersions(ctx, "x")
	checks["ProfileVersions"] = err
	_, err = p.SetDefaultProfile(ctx, "x")
	checks["SetDefaultProfile"] = err
	_, err = p.DefaultProfileName(ctx)
	checks["DefaultProfileName"] = err
	_, err = p.PromptUsers(ctx, "x")
	checks["PromptUsers"] = err
	_, err = p.ChannelOverrides(ctx, "youtube:UC1")
	checks["ChannelOverrides"] = err
	_, err = p.PatchChannelOverrides(ctx, "youtube:UC1", nil)
	checks["PatchChannelOverrides"] = err
	_, _, err = p.StageConfig(ctx, "i", StageCorrect)
	checks["StageConfig"] = err
	for name, err := range checks {
		var ni *NotImplementedError
		if !errors.As(err, &ni) {
			t.Errorf("%s = %v，預期 *NotImplementedError", name, err)
		}
	}
}
