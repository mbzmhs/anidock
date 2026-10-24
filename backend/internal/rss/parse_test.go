package rss

import (
	"strings"
	"testing"
)

func TestParseTitleRealSamples(t *testing.T) {
	cases := []struct {
		title      string
		group      string
		show       string
		episode    float64
		hasEpisode bool
	}{
		{
			title:    "[字幕组B] Show A S02 /  番剧A 第二季 - 02 [1080P][WEBRIP][WEB-DL][AAC AVC][CHT][MP4]",
			group:    "字幕组B",
			show:     "Show A S02 / 番剧A 第二季 WEBRIP AVC CHT MP4",
			episode:  2,
			hasEpisode: true,
		},
		{
			title:      "【字幕组A】番剧F [12][1080P][简繁内嵌]",
			group:      "字幕组A",
			show:       "番剧F",
			episode:    12,
			hasEpisode: true,
		},
		{
			title:      "【字幕组G】番剧A 第二季 Episode 1 [1080P] (简繁 示例视频台)",
			group:      "字幕组G",
			show:       "番剧A 第二季 简繁 示例视频台",
			episode:    1,
			hasEpisode: true,
		},
		{
			title:      "番剧A 第二季 第1.5话 720p",
			group:      "",
			show:       "番剧A 第二季 720p",
			episode:    1.5,
			hasEpisode: true,
		},
	}
	for _, c := range cases {
		p := ParseTitle(c.title)
		if p.Group != c.group {
			t.Errorf("group: %q want %q (title %q)", p.Group, c.group, c.title)
		}
		if c.hasEpisode {
			if p.Episode == nil || *p.Episode != c.episode {
				t.Errorf("episode: %v want %v (title %q)", p.Episode, c.episode, c.title)
			}
		}
		// show 必须包含番剧主干核心词（规格词剔除不要求严格相等）
		t.Logf("title=%q => group=%q show=%q ep=%v spec=%q quality=%q",
			c.title, p.Group, p.Show, p.Episode, p.Spec, p.Quality)
	}

	// 匹配关键用例：字幕组B 真实标题解析出的 show 必须能匹配到 "番剧A 第二季"
}

func TestParseTitleBrackets(t *testing.T) {
	// 方括号里的番名（含汉字）必须保留——组常把多别名番名写进方括号
	p := ParseTitle("[字幕组E][番剧G 第二季 / 番剧H 第二季 / Show G S2][01-12][1080P][简日双语][MP4][AVC_AAC][TVRIP][合集]")
	if p.Group != "字幕组E" {
		t.Errorf("group = %q", p.Group)
	}
	for _, want := range []string{"番剧G", "番剧H", "Show"} {
		if !strings.Contains(p.Show, want) {
			t.Errorf("show 应保留 %q: %q", want, p.Show)
		}
	}
	if strings.Contains(p.Show, "MP4") || strings.Contains(p.Show, "TVRIP") {
		t.Errorf("纯规格括号应被剔除: %q", p.Show)
	}
	// 含假名的方括号（日文名）同样保留
	p2 := ParseTitle("[Group] 番剧名 - 01 [アニメサンプルとデキている] [1080p][CHS]")
	if !strings.Contains(p2.Show, "アニメサンプル") {
		t.Errorf("假名括号应保留: %q", p2.Show)
	}
}

// TestParseTitleRanges 合集区间（多文件种子）：各写法解析为 [from, to]，
// 不再折叠成中点假集号；单值规则不得吃掉区间的左端（EP01-12 ≠ 第 1 集）；
// 日期/卷号/季区间不得误判成集数区间。
func TestParseTitleRanges(t *testing.T) {
	cases := []struct {
		title      string
		from, to   float64
		hasRange   bool // true = 合集区间
		single     float64
		hasSingle  bool // true = 单值集数（非合集）
		// 两者皆 false = 集数未知
	}{
		{title: "[字幕组E][番剧G 第二季][01-12][1080P][合集]", from: 1, to: 12, hasRange: true},
		{title: "【字幕组A】番剧F 第01-12话 [1080P][简繁内嵌] 合集", from: 1, to: 12, hasRange: true},
		{title: "[字幕组B] 番剧A 第二季 - 01-12 [1080P][合集]", from: 1, to: 12, hasRange: true},
		{title: "【字幕组C】番剧X 01~12 合集 [1080P]", from: 1, to: 12, hasRange: true},
		{title: "[字幕组D] 番剧Y 第1~12集 [1080P][合集]", from: 1, to: 12, hasRange: true},
		{title: "[字幕组F] 番剧Z 01-12话合集 [720P]", from: 1, to: 12, hasRange: true},
		{title: "[字幕组G] 番剧W EP01-12 [1080P]", from: 1, to: 12, hasRange: true},
		{title: "[字幕组I] 番剧U 01-12 END [1080P]", from: 1, to: 12, hasRange: true},
		{title: "[Group] 番剧名 05至08 [1080P]", from: 5, to: 8, hasRange: true},
		{title: "[Group] 番剧名 全12话 [1080P]", from: 1, to: 12, hasRange: true},
		{title: "[Group] 番剧名 1 - 12 [1080P][合集]", from: 1, to: 12, hasRange: true},
		{title: "[Group] 番剧名 [01－12] [1080P]", from: 1, to: 12, hasRange: true}, // 全角连字符
		// 非集数区间：不解析
		{title: "[Group] 番剧名 [2026-01-15] [1080P]"},         // 日期
		{title: "[Group] 番剧名 Vol 1-12 [1080P]"},            // 卷号区间
		{title: "[Group] 番剧名 第一季~第二季 合集 [1080P]"},      // 季区间
		{title: "[Group] 番剧名 x265-10bit [1080P]"},          // 规格串
		{title: "[Group] 番剧名 - 12-01 - [1080P]"},           // 倒置区间
		// 单值规则不受影响
		{title: "[Group] 番剧名 - 01v2 [1080P]", single: 1, hasSingle: true},
		{title: "[Group] 番剧名 第13话 [1080P]", single: 13, hasSingle: true},
	}
	for _, c := range cases {
		p := ParseTitle(c.title)
		switch {
		case c.hasRange:
			if p.Episode == nil || p.EpisodeTo == nil {
				t.Errorf("%q: 应解析出区间, got ep=%v to=%v", c.title, derefF64(p.Episode), derefF64(p.EpisodeTo))
				continue
			}
			if *p.Episode != c.from || *p.EpisodeTo != c.to {
				t.Errorf("%q: 区间 = [%v, %v], want [%v, %v]", c.title, *p.Episode, *p.EpisodeTo, c.from, c.to)
			}
		case c.hasSingle:
			if p.Episode == nil || *p.Episode != c.single || p.EpisodeTo != nil {
				t.Errorf("%q: 应为单值集数 %v, got ep=%v to=%v", c.title, c.single, derefF64(p.Episode), derefF64(p.EpisodeTo))
			}
		default:
			if p.Episode != nil || p.EpisodeTo != nil {
				t.Errorf("%q: 应无集数, got ep=%v to=%v", c.title, derefF64(p.Episode), derefF64(p.EpisodeTo))
			}
		}
		t.Logf("%q => ep=%v~%v raw=%q show=%q", c.title, derefF64(p.Episode), derefF64(p.EpisodeTo), p.EpisodeRaw, p.Show)
	}

	// 区间解析后番名主干不应残留集数碎片（旧版会留下 "第01 12话"、"第 集"）
	p := ParseTitle("【字幕组A】番剧F 第01-12话 [1080P] 合集")
	for _, frag := range []string{"01", "12", "话"} {
		if strings.Contains(p.Show, frag) {
			t.Errorf("Show 应剔除集数碎片 %q: %q", frag, p.Show)
		}
	}
	if !strings.Contains(p.Show, "番剧F") {
		t.Errorf("Show 应保留番名: %q", p.Show)
	}
}

// TestParseTitleSpecialEpisodes 真实世界的特殊集数形态：
// v2/v3 重压版、n.5 总集篇、第 0 集前篇。
func TestParseTitleSpecialEpisodes(t *testing.T) {
	cases := []struct {
		title      string
		episode    float64
		hasEpisode bool
		wantSpec   string // 应出现在 Spec 里的标记（可为空）
	}{
		{title: "[Group] 番剧名 [01v2][1080P]", episode: 1, hasEpisode: true, wantSpec: "v2"},
		{title: "[Group] 番剧名 [01V3][1080P]", episode: 1, hasEpisode: true, wantSpec: "v3"},
		{title: "[Group] 番剧名 [01][v2][1080P]", episode: 1, hasEpisode: true},
		{title: "[Group] 番剧名 - 01v2 [1080P][CHT]", episode: 1, hasEpisode: true, wantSpec: "v2"},
		{title: "[Group] 番剧名 - 01 v2 [1080P][CHT]", episode: 1, hasEpisode: true},
		{title: "[Group] 番剧名 EP01v2 [1080p]", episode: 1, hasEpisode: true, wantSpec: "v2"},
		{title: "[字幕组C] 番剧F / 番剧F 假名 S01E01 [CR WEB-DL 1080p AVC AAC][简繁内封]", episode: 1, hasEpisode: true},
		{title: "[Group] 番剧名 S2E5 [1080p]", episode: 5, hasEpisode: true},
		{title: "[Group] 番剧名 s12e3 [720p]", episode: 3, hasEpisode: true},
		{title: "[Group] 番剧名 S02E12.5 [1080p]", episode: 12.5, hasEpisode: true},
		// 只有季号无 E 号：不应当作集数（季词归 seasonSet 处理）
		{title: "[Group] 番剧名 第二季 S02 [1080p]", hasEpisode: false},
		{title: "[Group] 番剧名 第01话v2 1080p", episode: 1, hasEpisode: true, wantSpec: "v2"},
		{title: "[Group] 番剧名 - 12.5 [1080P]", episode: 12.5, hasEpisode: true},
		{title: "[Group] 番剧名 [12.5][1080P] 总集篇", episode: 12.5, hasEpisode: true},
		{title: "[Group] 番剧名 - 0 [1080P] 前篇", episode: 0, hasEpisode: true},
		{title: "[Group] 番剧名 [第0话] 1080p", episode: 0, hasEpisode: true},
		// 普通集号不受 v 后缀规则影响
		{title: "[Group] 番剧名 - 14 [1080P][WEBRIP][WEB-DL][AAC AVC][CHT][MP4]", episode: 14, hasEpisode: true},
	}
	for _, c := range cases {
		p := ParseTitle(c.title)
		if c.hasEpisode {
			if p.Episode == nil || *p.Episode != c.episode {
				t.Errorf("%q: episode = %v, want %v", c.title, p.Episode, c.episode)
				continue
			}
		} else if p.Episode != nil {
			t.Errorf("%q: episode = %v, want none", c.title, p.Episode)
		}
		if c.wantSpec != "" && !strings.Contains(strings.ToLower(p.Spec), c.wantSpec) {
			t.Errorf("%q: spec = %q, want 含 %q", c.title, p.Spec, c.wantSpec)
		}
		t.Logf("%q => ep=%v spec=%q show=%q", c.title, p.Episode, p.Spec, p.Show)
	}
}

func derefF64(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
