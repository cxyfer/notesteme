package app

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/cxyfer/notesteme/internal/store"
)

// TestControls.Migrations 傳到 store.Open：只提供前 4 支 migration 時，第 5 支才建立的 events 表不存在。
func TestControlsMigrationsReachStore(t *testing.T) {
	custom := fstest.MapFS{}
	for _, name := range []string{"00001_init.sql", "00002_encryption_keys.sql", "00003_items_pipeline.sql", "00004_secrets_resume.sql"} {
		data, err := fs.ReadFile(store.MigrationsFS(), name)
		if err != nil {
			t.Fatal(err)
		}
		custom[name] = &fstest.MapFile{Data: data}
	}
	env := testEnv(t)
	env.Test.Migrations = custom
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	if _, err := srv.store.Write().CountEvents(t.Context()); err == nil {
		t.Fatal("TestControls.Migrations 沒有生效：events 表不該存在")
	}
}

// 沒有設定 Migrations 時用內建的全部 migration。
func TestDefaultMigrationsHaveEvents(t *testing.T) {
	srv, err := StartServer(t.Context(), testEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	if _, err := srv.store.Write().CountEvents(t.Context()); err != nil {
		t.Fatalf("預設應有 events 表：%v", err)
	}
}
