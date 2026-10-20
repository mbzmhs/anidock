package model

// InfoboxItem Bangumi infobox 里的一条制作信息（制作公司/人员/平台/官网等）。
type InfoboxItem struct {
	Key    string   `json:"key"`
	Value  string   `json:"value,omitempty"`  // 单值
	Values []string `json:"values,omitempty"` // 多值（如别名）
}

// Seyuu 声优。Ver = Bangumi person-characters 的版本标识：
// 0 = 本作品（动画正片）配音，非 0 = 其它版本（真人版/配音版等，官方未公开常量含义，UI 统称"其它版本"）。
type Seyuu struct {
	Name string `json:"name"`
	Ver  int    `json:"ver"`
}

// Cast 角色 + 该角色的声优（Bangumi Archive 本地关联，存 anime_cast 表）。
// Lead = 角色定位（Bangumi subject-characters type）：1=主角 2=配角 3=客串，0/其它=未标注。
type Cast struct {
	Name  string  `json:"name"`
	Lead  int     `json:"lead"`
	Seyuu []Seyuu `json:"seiyuu,omitempty"`
}

// Anime 番剧（静态元数据）。Genres/Images/Aliases/Info/Characters 以 JSON 持久化。
type Anime struct {
	ID         int64    `json:"id"`
	BangumiID  *int64   `json:"bangumi_id"`
	Title      string   `json:"title"`
	TitleOrig  string   `json:"title_orig"`
	Synopsis   string   `json:"synopsis"`
	Year       int      `json:"year"`
	Quarter    int      `json:"quarter"`
	SeasonLabel string  `json:"season_label"`
	Genres     []string `json:"genres"`
	Cover      string   `json:"cover"`
	Poster     string   `json:"poster"`
	Images     []string `json:"images"`
	Aliases    []string `json:"aliases"`
	Studio     string   `json:"studio,omitempty"`
	Info       []InfoboxItem `json:"info,omitempty"`
	Characters []string `json:"characters,omitempty"`
	Cast       []Cast   `json:"cast,omitempty"` // 角色/声优（运行时联查，详情页展示）
	Source     string   `json:"source"`
	ImportedAt string   `json:"imported_at"`
	UpdatedAt  string   `json:"updated_at"`

	// 列表摘要（运行时聚合，非持久化）
	LatestEpisode   float64 `json:"latest_episode,omitempty"`
	LatestGroup     string  `json:"latest_group,omitempty"`
	LatestGroupColor string `json:"latest_group_color,omitempty"`
	LatestUpdatedAt string  `json:"latest_updated_at,omitempty"`
	EpisodeCount    int     `json:"episode_count,omitempty"`
	FeedCount       int     `json:"feed_count,omitempty"`
}

// AnimeDraft 元数据导入器产出的草稿（可插拔来源的统一格式，见 DESIGN §5.1/§5.2）。
type AnimeDraft struct {
	BangumiID   *int64
	Title       string
	TitleOrig   string
	Synopsis    string
	Year        int
	Quarter     int
	SeasonLabel string
	Genres      []string
	Cover       string
	Poster      string
	Images      []string
	Aliases     []string
	Studio      string
	Info        []InfoboxItem
	Characters  []string
	Cast        []Cast // 角色/声优（归档导入时本地关联产出；API 快照导入为空）
	Source      string
}

// Feed 字幕组 RSS 源。
// Site = 订阅地址所属站点（真实域名，去 www；手动源可能为空）；Keyword = 站点无关订阅关键词
// （自动发现产出；兄弟站同关键词物化各自的镜像 feed，抓取时同组同集自动合行）。
type Feed struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	RSSURL        string  `json:"rss_url"`
	BaseURL       string  `json:"base_url"`
	AnimeID       *int64  `json:"anime_id"`
	Priority      int     `json:"priority"`
	Enabled       bool    `json:"enabled"`
	Mirror        bool    `json:"mirror"` // 开启后新资源文件自动下载镜像到本地
	Site          string  `json:"site,omitempty"`
	Keyword       string  `json:"keyword,omitempty"`
	Color         string  `json:"color"`
	LastFetchedAt *string `json:"last_fetched_at"`
	LastError     string  `json:"last_error"`
	CreatedAt     string  `json:"created_at"`
}

// Release 资源条目（来自 RSS）。
type Release struct {
	ID          int64   `json:"id"`
	FeedID      int64   `json:"feed_id"`
	AnimeID     *int64  `json:"anime_id"`
	Title       string  `json:"title"`
	Episode     *float64 `json:"episode"`
	URL         string  `json:"url"`
	DownloadURL string  `json:"download_url"`
	Quality     string  `json:"quality"`
	Spec        string  `json:"spec"`
	GroupName   string  `json:"group_name"`
	PublishedAt string  `json:"published_at"`
	FetchedAt   string  `json:"fetched_at"`

	// 联查展示字段
	FeedName     string `json:"feed_name,omitempty"`
	FeedPriority int    `json:"feed_priority,omitempty"`
	FeedColor    string `json:"feed_color,omitempty"`

	// 本地镜像（校验通过、原子落盘后才有值；非空表示可无感切换为本地下载）
	MirrorPath string `json:"mirror_path,omitempty"`
	MirrorSize int64  `json:"mirror_size,omitempty"`
}

// EpisodeView 某集的去重结果：best = 该集展示的"最优"release，alternatives = 其余源。
type EpisodeView struct {
	Episode      float64 `json:"episode"`
	Best         Release `json:"best"`
	Alternatives []Release `json:"alternatives,omitempty"`
}

// AnimeFilter 前台列表筛选条件（q 为关键词搜索：标题/别名/关键词 命中即中）。
type AnimeFilter struct {
	Year    int    `json:"year"`
	Quarter int    `json:"quarter"`
	Q       string `json:"q"`
	Sort    string `json:"sort"`  // "" / "season" = 按年度季度（默认）；"latest" = 按最新更新
	HasEps   bool   `json:"has_eps"` // 只显示已有剧集（至少 1 条 release）的番剧
	HasFeeds bool   `json:"has_feeds"` // 只显示已有 RSS 源的番剧（管理页筛选）
	Ids      []int64 `json:"-"` // 按 id 批量筛选（外部客户端一次拉多部；不出现在 JSON 里）
}
