package fakecompose

import (
	"sort"
	"testing"
)

// 真正的 deploy/docker/compose.yaml：每個服務的每個 port 都要綁在 127.0.0.1。
// 場景的 jsonSubset 只比對陣列前綴，看不到第二個 port；新增的 port 沒綁 loopback 會直接對外開放，所以在這裡逐一檢查。
func TestRealComposeEveryPortIsBoundToLoopback(t *testing.T) {
	root, err := FindRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOTESTEME_MASTER_KEY": "fake-key"}
	for name, profiles := range map[string][]string{"預設": nil, "wsl": {"wsl"}} {
		got := runJSON(t, root, configArgs("deploy/docker/compose.yaml", profiles...), env)
		services, _ := got["services"].(map[string]any)
		if len(services) == 0 {
			t.Fatalf("%s：compose 沒有任何服務", name)
		}
		names := make([]string, 0, len(services))
		for n := range services {
			names = append(names, n)
		}
		sort.Strings(names)

		total := 0
		for _, svcName := range names {
			svc, _ := services[svcName].(map[string]any)
			ports, _ := svc["ports"].([]any)
			for i, p := range ports {
				total++
				port, _ := p.(map[string]any)
				if port["host_ip"] != "127.0.0.1" {
					t.Errorf("%s：服務 %s 的第 %d 個 port 沒有綁在 127.0.0.1：%v", name, svcName, i+1, p)
				}
			}
		}
		if total == 0 {
			t.Errorf("%s：沒有任何 port，檢查等於沒做", name)
		}
	}
}
