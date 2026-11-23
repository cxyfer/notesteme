package hubrepo

// 模型欄位與 model_verified（HREP-R7、ADR-0031 第 3 點）：一個版本的三個模型欄位沿著衍生關係取得——
// 自己這一步（source、corrected、translated 的字幕）加上 parents_sha256 指到的上游（同一個 bundle 中的產出，
// 或 Hub 上同一部影片已有的版本）；上游任何一步未驗證，這個版本就是 model_verified: false。

// Models 是版本的篩選欄位。
type Models struct {
	Transcribe, Correct, Translate string
	Verified                       bool
}

// Node 是 bundle 中的一個產出。
type Node struct {
	Kind, Variant, SHA256 string
	Prov                  Provenance
}

// Lookup 依 sha256 找 Hub 上同一部影片已有的版本的模型欄位。
type Lookup func(sha string) (Models, bool)

// OriginVerified 回報 hub_origin 指到的版本是否 model_verified；找不到時回 false（保守）。
type OriginVerified func(versionID string) bool

// ComputeModels 依 nodes 的順序回傳每個產出的模型欄位。
func ComputeModels(nodes []Node, hub Lookup, origin OriginVerified) []Models {
	bySHA := map[string]int{}
	for i, n := range nodes {
		if _, ok := bySHA[n.SHA256]; !ok {
			bySHA[n.SHA256] = i
		}
	}
	memo := map[int]Models{}
	var visit func(i int, path map[int]bool) Models
	visit = func(i int, path map[int]bool) Models {
		if m, ok := memo[i]; ok {
			return m
		}
		n := nodes[i]
		var m Models
		if n.Kind == "transcript" {
			switch n.Variant {
			case "source":
				m.Transcribe = n.Prov.TranscribeModel()
			case "corrected":
				m.Correct = n.Prov.StepModel()
			case "translated":
				m.Translate = n.Prov.StepModel()
			}
		}
		m.Verified = !n.Prov.Unverified()
		if n.Prov.Executor == "hub" && (origin == nil || !origin(n.Prov.HubOrigin.VersionID)) {
			m.Verified = false
		}
		path[i] = true
		for _, sha := range n.Prov.ParentsSHA256 {
			var pm Models
			if j, ok := bySHA[sha]; ok && j != i {
				if path[j] {
					continue // 循環的 parents：不再往上走
				}
				pm = visit(j, path)
			} else if hub != nil {
				var found bool
				if pm, found = hub(sha); !found {
					continue
				}
			} else {
				continue
			}
			m.Transcribe = first(m.Transcribe, pm.Transcribe)
			m.Correct = first(m.Correct, pm.Correct)
			m.Translate = first(m.Translate, pm.Translate)
			m.Verified = m.Verified && pm.Verified
		}
		delete(path, i)
		memo[i] = m
		return m
	}
	out := make([]Models, len(nodes))
	for i := range nodes {
		out[i] = visit(i, map[int]bool{})
	}
	return out
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
