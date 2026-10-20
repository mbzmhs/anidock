package disc

import (
	"sort"
	"strings"
)

// Rank 自动添加的候选排序（组合逻辑，偏好表来自设置页，可为空）：
//  1. 偏好层：候选的组名/关键词包含某偏好词 → 按该偏好词在偏好列表中的位置分层
//     （越靠前优先级越高，大小写不敏感）；未命中任何偏好词的候选全部排在最后；
//  2. 兜底：集数多的靠前；
//  3. 最后：首次发布日期早的靠前（FirstPub 为 "2006-01-02" 格式，字典序即可）。
//
// 返回新切片，不改原切片；同层内保持原始相对顺序（稳定排序）。
func Rank(cands []Candidate, prefs []string) []Candidate {
	out := make([]Candidate, len(cands))
	copy(out, cands)
	prefLC := make([]string, len(prefs))
	for i, p := range prefs {
		prefLC[i] = strings.ToLower(strings.TrimSpace(p))
	}
	tier := func(c Candidate) int {
		g, k := strings.ToLower(c.Group), strings.ToLower(c.Keyword)
		for i, p := range prefLC {
			if p == "" {
				continue
			}
			if containsAny(g, k, p) {
				return i
			}
		}
		return len(prefs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := tier(out[i]), tier(out[j])
		if ti != tj {
			return ti < tj
		}
		ei, ej := len(out[i].Episodes), len(out[j].Episodes)
		if ei != ej {
			return ei > ej
		}
		return out[i].FirstPub < out[j].FirstPub
	})
	return out
}

// containsAny 组名或关键词包含词（大小写不敏感）。
func containsAny(group, keyword, term string) bool {
	return strings.Contains(group, term) || strings.Contains(keyword, term)
}

// BlackHit 候选的组名/关键词命中黑名单任一词（大小写不敏感）→ 自动添加时排除。
func BlackHit(c Candidate, black []string) bool {
	g, k := strings.ToLower(c.Group), strings.ToLower(c.Keyword)
	for _, b := range black {
		if b = strings.ToLower(strings.TrimSpace(b)); b != "" && containsAny(g, k, b) {
			return true
		}
	}
	return false
}
