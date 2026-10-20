package importer

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anidock/backend/internal/model"
)

// Bangumi 快照适配器（DESIGN §5.3/§5.4）：
// GET {base}/v0/subjects/{id}（Bearer token），只做首次快照；
// 封面 + 全部相关图下载后按 GUID 命名落地 media/，之后与 Bangumi 再无关系。
type Bangumi struct {
	BaseURL  string
	Token    string
	MediaDir string
	client   *http.Client
	// 运行时覆盖（后台「设置」改 token/地址后无需重启即生效）；返回空串 = 用上方静态值
	TokenFunc func() string
	BaseFunc  func() string
}

// cred 当前生效的 (token, baseURL)：优先 TokenFunc/BaseFunc（后台设置），回落静态配置。
func (b *Bangumi) cred() (token, base string) {
	token, base = b.Token, b.BaseURL
	if b.BaseFunc != nil {
		if s := b.BaseFunc(); s != "" {
			base = strings.TrimRight(s, "/")
		}
	}
	if b.TokenFunc != nil {
		if s := b.TokenFunc(); s != "" {
			token = s
		}
	}
	return token, base
}

// bangumiUA Bangumi API 明确禁止默认 Go User-Agent（Go-http-client 会 403），
// 必须自定义（见 bangumi/api docs-raw/user agent）。
const bangumiUA = "anidock/1.0 (RSS anime aggregator; bangumi v0 API client)"

// uaRoundTripper 为所有出站请求（API + 图片下载共用同一 client）强制设置 User-Agent。
type uaRoundTripper struct{ base http.RoundTripper }

func (t *uaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("User-Agent", bangumiUA)
	return t.base.RoundTrip(r)
}

func NewBangumi(baseURL, token, mediaDir string) *Bangumi {
	return &Bangumi{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Token:    token,
		MediaDir: mediaDir,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &uaRoundTripper{
				base: http.DefaultTransport,
			},
		},
	}
}

func (b *Bangumi) Name() string { return "bangumi" }

// bangumiSubject Bangumi v0 subject 实际字段（注意：没有 season/genre 字段，
// 年份季度要从 date（首播日期）推导，分类信息在 tags 里）。
type bangumiSubject struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Type     int    `json:"type"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Summary  string `json:"summary"`
	Platform string            `json:"platform"`
	Images   map[string]string `json:"images"` // {"large":"https://…", "medium":"…", ...}
	Genres   []string          `json:"genres"` // 个别条目才有，兜底保留
	Tags     []struct {
		Name string `json:"name"`
	} `json:"tags"`
	YearMin int `json:"year_min"` // 个别条目才有，date 缺失时兜底
	Infobox []bangumiInfobox `json:"infobox"` // 制作信息：公司/人员/平台/官网/别名/话数…
}

type bangumiInfobox struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"` // 字符串或 [{v:"…"}] 多值
}

// studioKeys 按优先级识别"动画制作公司"的 infobox key
var studioKeys = []string{"动画制作", "動画制作", "アニメーション制作", "製作"}

// parseInfobox 规范化 infobox 条目，并顺带提取制作公司与别名。
func parseInfobox(in []bangumiInfobox) (items []model.InfoboxItem, studio string, aliases []string) {
	items = []model.InfoboxItem{}
	for _, ib := range in {
		item := model.InfoboxItem{Key: strings.TrimSpace(ib.Key)}
		switch v := ib.Value.(type) {
		case string:
			item.Value = strings.TrimSpace(v)
		case []interface{}:
			for _, x := range v {
				if m, ok := x.(map[string]interface{}); ok {
					if s, ok := m["v"].(string); ok && strings.TrimSpace(s) != "" {
						item.Values = append(item.Values, strings.TrimSpace(s))
					}
				}
			}
		}
		if item.Value == "" && len(item.Values) == 0 {
			continue
		}
		items = append(items, item)
		if studio == "" {
			for _, k := range studioKeys {
				if item.Key == k && item.Value != "" {
					studio = item.Value
					break
				}
			}
		}
		if item.Key == "别名" {
			aliases = append(aliases, item.Values...)
		}
	}
	return items, studio, aliases
}

// fetchCharacters 尽力获取角色名，失败返回 nil（限 40 个）
func (b *Bangumi) fetchCharacters(id int64) []string {
	_, base := b.cred()
	resp, err := b.client.Get(fmt.Sprintf("%s/v0/subjects/%d/characters", base, id))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	type ch struct {
		Name string `json:"name"`
	}
	var list []ch
	var env struct {
		Data []ch `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Data) > 0 {
		list = env.Data
	} else if err := json.Unmarshal(body, &list); err != nil {
		return nil
	}
	var names []string
	for _, c := range list {
		if n := strings.TrimSpace(c.Name); n != "" {
			names = append(names, n)
			if len(names) >= 40 {
				break
			}
		}
	}
	return names
}

// Resolve 按 bangumi_id 拉快照 → 下载图片（GUID 命名）→ 映射为 AnimeDraft。
func (b *Bangumi) Resolve(query string) (*model.AnimeDraft, error) {
	token, base := b.cred()
	u := fmt.Sprintf("%s/v0/subjects/%s", base, strings.TrimSpace(query))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Bangumi: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Bangumi 返回 %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var subj bangumiSubject
	if err := json.Unmarshal(body, &subj); err != nil {
		return nil, fmt.Errorf("解析 Bangumi 响应: %w", err)
	}
	if subj.ID == 0 {
		return nil, fmt.Errorf("Bangumi 未找到该条目: %s", query)
	}

	// 年份/季度：v0 接口没有 season 字段，用首播日期 date 推导（月份→季度）；
	// date 缺失时回退 year_min（无季度信息）。
	year, quarter := 0, 0
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(subj.Date)); err == nil {
		year = t.Year()
		quarter = int((t.Month()-1)/3) + 1
	} else if subj.YearMin > 0 {
		year = subj.YearMin
	}

	// infobox → 制作信息（公司/人员/平台/官网…）+ 制作公司 + 别名
	infoItems, studio, aliasTags := parseInfobox(subj.Infobox)

	// 别名：infobox 别名 + 中英文名（去重；供 RSS 标题匹配）
	aliases := []string{}
	for _, s := range append(append([]string{}, aliasTags...), subj.NameCN, subj.Name) {
		if s == "" {
			continue
		}
		dup := false
		for _, x := range aliases {
			if x == s {
				dup = true
				break
			}
		}
		if !dup {
			aliases = append(aliases, s)
		}
	}
	if aliases == nil {
		aliases = []string{}
	}

	// tags 全量保留：Bangumi 的标签原样落库（不做降噪），用于展示与关键词搜索命中
	tagNames := make([]string, 0, len(subj.Tags))
	for _, t := range subj.Tags {
		if name := strings.TrimSpace(t.Name); name != "" {
			tagNames = append(tagNames, name)
		}
	}

	draft := &model.AnimeDraft{
		BangumiID:    &subj.ID,
		Title:        subj.NameCN,
		TitleOrig:    subj.Name,
		Synopsis:     subj.Summary,
		Year:         year,
		Quarter:      quarter,
		SeasonLabel:  seasonLabel(year, quarter),
		Genres:       mergeGenres(append(tagNames, subj.Genres...), subj.Platform),
		Aliases:      aliases,
		Studio:       studio,
		Info:         infoItems,
		Characters:   b.fetchCharacters(subj.ID),
		Source:       "bangumi",
	}
	if draft.Title == "" {
		draft.Title = subj.Name
	}
	if draft.Characters == nil {
		draft.Characters = []string{}
	}

	// 图片：Bangumi 的 large/medium/common/small/grid 均为同一张封面的不同缩放，
	// 只取 large（最大）作封面；无 large 时依次回退 grande/medium。
	coverURL := subj.Images["large"]
	if coverURL == "" {
		coverURL = subj.Images["grande"]
	}
	if coverURL == "" {
		coverURL = subj.Images["medium"]
	}
	if coverURL != "" {
		local, err := b.download(coverURL)
		if err != nil {
			return nil, fmt.Errorf("下载封面 %s: %w", coverURL, err)
		}
		draft.Cover = local
	}
	return draft, nil
}

// Ping 用最小子目请求验证 API 地址/token 是否可用（后台设置页「测试」按钮）。
func (b *Bangumi) Ping() error {
	token, base := b.cred()
	req, err := http.NewRequest(http.MethodGet, base+"/v0/subjects/1", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 Bangumi: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
}

// download 下载远程图片，按 md5 内容哈希作文件名（等效 GUID：稳定、抗覆盖、去重）。
func (b *Bangumi) download(url string) (string, error) {
	if err := os.MkdirAll(b.MediaDir, 0o755); err != nil {
		return "", err
	}
	resp, err := b.client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return "", err
	}
	sum := md5.Sum(data)
	name := hex.EncodeToString(sum[:]) + extFromURL(url)
	path := filepath.Join(b.MediaDir, name)
	if _, err := os.Stat(path); err == nil {
		return "media/" + name, nil // 已存在（同一 Bangumi 图 URL 共享）
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return "media/" + name, nil
}

func extFromURL(url string) string {
	lower := strings.ToLower(url)
	if i := strings.Index(lower, "?"); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case strings.HasSuffix(lower, ".png"):
		return ".png"
	case strings.HasSuffix(lower, ".webp"):
		return ".webp"
	case strings.HasSuffix(lower, ".gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}

func seasonLabel(year, quarter int) string {
	if year <= 0 {
		return ""
	}
	switch quarter {
	case 1:
		return fmt.Sprintf("%d-1月", year)
	case 2:
		return fmt.Sprintf("%d-4月", year)
	case 3:
		return fmt.Sprintf("%d-7月", year)
	case 4:
		return fmt.Sprintf("%d-10月", year)
	default:
		return fmt.Sprintf("%d", year)
	}
}

// mergeGenres 分类标签（tags/genres）+ platform（TV/电影等并入 genres，不单独存 format）。
func mergeGenres(genres []string, platform string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if platform != "" {
		add(platform)
	}
	for _, g := range genres {
		add(g)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
