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
			title:      "【字幕组G】番剧A 第二季 Episode 1 [1080P] (简繁 BILIBILI)",
			group:      "字幕组G",
			show:       "番剧A 第二季 简繁 BILIBILI",
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
