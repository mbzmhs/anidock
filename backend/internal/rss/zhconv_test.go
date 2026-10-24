package rss

import "testing"

// TestSimplifyMultiTargetPhrase OpenCC 词典的"多候选译名"词条（目标含空格）
// 只取第一个候选，不得把空格后的候选一并写入污染文本。
func TestSimplifyMultiTargetPhrase(t *testing.T) {
	cases := map[string]string{
		"龍鍾老人":     "龙钟老人",
		"情有獨鍾":     "情有独钟",
		"魏徵與想像力":   "魏徵与想像力", // 多目标词条自身保持确定性别名（魏徵→魏徵）
		"乾隆乾坤":     "乾隆乾坤", // 身份词条：乾隆/乾坤 不简化
	}
	for in, want := range cases {
		if got := Simplify(in); got != want {
			t.Errorf("Simplify(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMatcherNormalizeFullTable 入库匹配器的归一化应覆盖全量繁简表
// （旧版 40 字手工表覆盖不到的字：櫻/聽/緣等）。
func TestMatcherNormalizeFullTable(t *testing.T) {
	pairs := [][2]string{
		{"櫻色少女", "樱色少女"},
		{"聽雪之樓", "听雪之楼"},
		{"緣之空", "缘之空"},
	}
	for _, p := range pairs {
		a, b := normalize(p[0]), normalize(p[1])
		if a != b {
			t.Errorf("normalize(%q)=%q 与 normalize(%q)=%q 应一致", p[0], a, p[1], b)
		}
	}
	// 全角数字/字母也应归一
	if normalize("ＦＵＬＬ－１") != "full1" {
		t.Errorf("全角归一异常: %q", normalize("ＦＵＬＬ－１"))
	}
}
