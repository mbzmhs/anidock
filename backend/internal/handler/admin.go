package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"anidock/backend/config"
	"anidock/backend/internal/importer"
	"anidock/backend/internal/model"
	"anidock/backend/internal/rss"
	"anidock/backend/internal/rss/disc"
	"anidock/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

// Admin 后台接口（单口令鉴权 → 短期 token，DESIGN §9.2）。
type Admin struct {
	Store      *store.Store
	Importer   *importer.Registry
	Fetcher    *rss.Fetcher
	Cfg        *config.Config
	tokens     *TokenStore // 后台（admin_token）
	visitors   *TokenStore // 访客（visitor_token）
	limiter    *loginLimiter
	discBusy   atomic.Int32       // 自动发现全局互斥：同一时刻最多一个后台发现任务
	bulkBusy   atomic.Int32       // 批量发现全局互斥：同一时刻最多一个批量任务
	bulkMu     sync.Mutex         // 保护 bulkCancel
	bulkCancel context.CancelFunc // 进行中批量任务的停止函数（nil = 无任务）
}

// tokenTTLFunc 后台/访客共用的 token 有效期（settings 热改；"0" = 无限制）。
func (a *Admin) tokenTTLFunc() time.Duration {
	return EffectiveTokenTTL(a.Store, a.Cfg)
}

func NewAdmin(st *store.Store, reg *importer.Registry, f *rss.Fetcher, cfg *config.Config) *Admin {
	a := &Admin{Store: st, Importer: reg, Fetcher: f, Cfg: cfg}
	a.tokens = NewTokenStore(st.DB, "admin_token", a.tokenTTLFunc)
	a.visitors = NewTokenStore(st.DB, "visitor_token", a.tokenTTLFunc)
	a.limiter = newLoginLimiter()
	return a
}

// ---------- 鉴权 ----------

// TokenStore 登录 token 存储（落库持久化：进程重启后未过期的 token 依然有效）。
// 后台（admin_token）与访客（visitor_token）各用一个实例；
// TTL 支持运行时热改（ttlFunc 返回 0 = 无限制，签发永久 token）。
type TokenStore struct {
	db      *sql.DB
	table   string
	ttlFunc func() time.Duration
}

func NewTokenStore(db *sql.DB, table string, ttlFunc func() time.Duration) *TokenStore {
	return &TokenStore{db: db, table: table, ttlFunc: ttlFunc}
}

func (t *TokenStore) ttl() time.Duration {
	if t.ttlFunc != nil {
		return t.ttlFunc()
	}
	return 24 * time.Hour
}

func (t *TokenStore) issue() (string, bool, error) { // (token, permanent, err)
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	now := time.Now()
	ttl := t.ttl()
	permanent := 0
	exp := ""
	if ttl <= 0 {
		permanent = 1 // 无限制
	} else {
		exp = now.Add(ttl).UTC().Format(time.RFC3339)
	}
	if _, err := t.db.Exec(fmt.Sprintf("INSERT OR REPLACE INTO %s (token, expires_at, permanent) VALUES (?, ?, ?)", t.table), token, exp, permanent); err != nil {
		// 无内存副本可回退，落库失败则让本次登录失败
		return "", false, fmt.Errorf("token 落库失败: %w", err)
	}
	// 顺手清掉过期 token
	_, _ = t.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE permanent=0 AND expires_at < ?", t.table), now.UTC().Format(time.RFC3339))
	return token, permanent == 1, nil
}

func (t *TokenStore) valid(token string) bool {
	if token == "" {
		return false
	}
	var exp string
	var permanent int
	if err := t.db.QueryRow(fmt.Sprintf("SELECT expires_at, permanent FROM %s WHERE token=?", t.table), token).Scan(&exp, &permanent); err != nil {
		return false
	}
	if permanent == 1 {
		return true
	}
	tm, err := time.Parse(time.RFC3339, exp)
	if err != nil {
		return false
	}
	if time.Now().After(tm) {
		_, _ = t.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE token=?", t.table), token)
		return false
	}
	return true
}

type loginReq struct {
	Password string `json:"password"`
}

// Login POST /api/admin/login
func (a *Admin) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	pw := EffectiveAdminPassword(a.Store, a.Cfg)
	if pw == "" {
		writeErr(w, 503, errors.New("服务端未配置 ADMIN_PASSWORD，后台不可用"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(pw)) != 1 {
		writeErr(w, 401, errors.New("口令错误"))
		return
	}
	token, permanent, err := a.tokens.issue()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	ttl := "无限制"
	if !permanent {
		ttl = a.tokenTTLFunc().String()
	}
	writeJSON(w, 200, map[string]string{"token": token, "expires_in": ttl})
}

// RequireAdmin 中间件：Authorization: Bearer <token>
func (a *Admin) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if auth == "" || token == auth || !a.tokens.valid(token) {
			writeErr(w, 401, errors.New("未登录或 token 已过期"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- 番剧管理 ----------

// ListAnime GET /api/admin/anime?year=&quarter=&q=&sort=&limit=&offset=
func (a *Admin) ListAnime(w http.ResponseWriter, r *http.Request) {
	f, limit, offset := parseAnimeListParams(r)
	list, err := a.Store.ListAnime(f, limit, offset)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.Anime{}
	}
	writeJSON(w, 200, list)
}

type animeReq struct {
	BangumiID   *int64              `json:"bangumi_id"`
	Title       string              `json:"title"`
	TitleOrig   string              `json:"title_orig"`
	Synopsis    string              `json:"synopsis"`
	Year        int                 `json:"year"`
	Quarter     int                 `json:"quarter"`
	SeasonLabel string              `json:"season_label"`
	Genres      []string            `json:"genres"`
	Cover       string              `json:"cover"`
	Poster      string              `json:"poster"`
	Images      []string            `json:"images"`
	Aliases     []string            `json:"aliases"`
	Studio      string              `json:"studio"`
	Info        []model.InfoboxItem `json:"info"`
	Characters  []string            `json:"characters"`
}

// CreateAnime POST /api/admin/anime（手动新建/编辑元数据）
func (a *Admin) CreateAnime(w http.ResponseWriter, r *http.Request) {
	var req animeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	if req.Title == "" {
		writeErr(w, 400, errors.New("title 不能为空"))
		return
	}
	id, err := a.Store.CreateAnime(&model.AnimeDraft{
		BangumiID: req.BangumiID, Title: req.Title, TitleOrig: req.TitleOrig, Synopsis: req.Synopsis,
		Year: req.Year, Quarter: req.Quarter, SeasonLabel: req.SeasonLabel,
		Genres: req.Genres, Cover: req.Cover, Poster: req.Poster, Images: req.Images,
		Aliases: req.Aliases, Studio: req.Studio, Info: req.Info, Characters: req.Characters, Source: "manual",
	})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	created, _ := a.Store.QueryAnimeByID(id)
	writeJSON(w, 200, created)
}

// UpdateAnime PUT /api/admin/anime/{id}
func (a *Admin) UpdateAnime(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	an, err := a.Store.QueryAnimeByID(id)
	if err != nil {
		writeErr(w, 404, errors.New("番剧不存在"))
		return
	}
	// 记录更新前的图片引用，供更新后清理失效文件
	oldImgs := &model.Anime{ID: an.ID, Cover: an.Cover, Poster: an.Poster, Images: an.Images}
	var req animeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	if req.Title != "" {
		an.Title = req.Title
	}
	// 编辑页总是提交完整表单：可空字段无条件覆盖（空串/空数组 = 清除）
	an.TitleOrig = req.TitleOrig
	an.Synopsis = req.Synopsis
	an.Year, an.Quarter = req.Year, req.Quarter
	an.SeasonLabel = req.SeasonLabel
	if req.Genres != nil {
		an.Genres = req.Genres
	}
	an.Cover = req.Cover
	an.Poster = req.Poster
	an.Images = req.Images
	an.Aliases = req.Aliases
	an.Studio = req.Studio
	an.Info = req.Info
	an.Characters = req.Characters
	if req.BangumiID != nil {
		an.BangumiID = req.BangumiID
	}
	if err := a.Store.UpdateAnime(an); err != nil {
		writeErr(w, 500, err)
		return
	}
	// 图片随数据持久化：被移除且不再被其它番剧引用的旧图自动删除
	a.cleanupAnimeImages(oldImgs, animeImagePaths(an), an.ID)
	writeJSON(w, 200, an)
}

// cleanupAnimeImages 删除 old 中引用、但 keep（更新后仍在用的路径）之外不再使用、
// 且未被其它番剧共享的本地图片文件。keep 为空 = 全部释放（删除番剧场景）。
func (a *Admin) cleanupAnimeImages(old *model.Anime, keep []string, exceptID int64) {
	if old == nil {
		return
	}
	keepSet := map[string]bool{}
	for _, p := range keep {
		keepSet[p] = true
	}
	dir, err := filepath.Abs(a.Cfg.MediaDir)
	if err != nil {
		return
	}
	oldPaths := append([]string{old.Cover, old.Poster}, old.Images...)
	for _, p := range oldPaths {
		if p == "" || keepSet[p] || !reMediaFile.MatchString(filepath.Base(p)) {
			continue
		}
		if n, err := a.Store.CountImageRefs(p, exceptID); err == nil && n == 0 {
			_ = os.Remove(filepath.Join(dir, filepath.Base(p)))
		}
	}
}

func animeImagePaths(a *model.Anime) []string {
	return append([]string{a.Cover, a.Poster}, a.Images...)
}

// cleanupMirrorFiles 删除不再被任何 release 引用的本地镜像文件（番剧/源删除时调用）。
func (a *Admin) cleanupMirrorFiles(paths []string) {
	if a.Cfg.MirrorDir == "" {
		return
	}
	dir, err := filepath.Abs(a.Cfg.MirrorDir)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range paths {
		base := filepath.Base(p)
		if p == "" || seen[base] || !reMirrorFile.MatchString(base) {
			continue
		}
		seen[base] = true
		// 仍被其它 release 引用（共享文件）则保留
		if n, err := a.Store.CountMirrorRefs(p); err == nil && n == 0 {
			_ = os.Remove(filepath.Join(dir, base))
		}
	}
}

// DeleteAnime DELETE /api/admin/anime/{id}
// 级联删除其名下 RSS 源及全部更新记录，并清理不再被引用的图片文件。
func (a *Admin) DeleteAnime(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	old, _ := a.Store.QueryAnimeByID(id)
	mirrorPaths, _ := a.Store.MirrorPathsForAnime(id)
	if err := a.Store.DeleteAnime(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	if old != nil {
		a.cleanupAnimeImages(old, nil, 0)
	}
	a.cleanupMirrorFiles(mirrorPaths)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Import POST /api/admin/anime/import {"source":"bangumi","query":"622288"}
func (a *Admin) Import(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Query  string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	imp, ok := a.Importer.Get(req.Source)
	if !ok {
		writeErr(w, 400, errors.New("未知导入来源: "+req.Source))
		return
	}
	draft, err := imp.Resolve(req.Query)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	// 重复导入（快照刷新）前记录旧图片引用，用于清理失效文件
	var old *model.Anime
	if draft.BangumiID != nil {
		old, _ = a.Store.ByBangumiID(*draft.BangumiID)
	}
	id, err := a.Store.CreateAnime(draft)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	created, _ := a.Store.QueryAnimeByID(id)
	if old != nil && created != nil {
		a.cleanupAnimeImages(old, animeImagePaths(created), old.ID)
	}
	writeJSON(w, 200, created)
}

// PreviewAnime POST /api/admin/anime/preview {"source":"bangumi","query":"554779"}
// 只拉取快照返回给前端预填表单，不写库；保存与否由用户决定。
func (a *Admin) PreviewAnime(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Query  string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeErr(w, 400, errors.New("query 不能为空"))
		return
	}
	imp, ok := a.Importer.Get(req.Source)
	if !ok {
		writeErr(w, 400, errors.New("未知导入来源: "+req.Source))
		return
	}
	draft, err := imp.Resolve(req.Query)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"bangumi_id":   draft.BangumiID,
		"title":        draft.Title,
		"title_orig":   draft.TitleOrig,
		"synopsis":     draft.Synopsis,
		"year":         draft.Year,
		"quarter":      draft.Quarter,
		"season_label": draft.SeasonLabel,
		"genres":       draft.Genres,
		"aliases":      draft.Aliases,
		"studio":       draft.Studio,
		"info":         draft.Info,
		"characters":   draft.Characters,
		"cover":        draft.Cover,
		"poster":       draft.Poster,
		"images":       draft.Images,
	})
}

// ImportArchive POST /api/admin/anime/import-archive
// {"dir":"/path/to/archive","year_from":2026,"year_to":2026,"only_tv":false,"fetch_covers":true}
// 启动后台批量导入（Bangumi Archive jsonlines），按 bangumi_id 去重跳过已存在条目。
func (a *Admin) ImportArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir         string   `json:"dir"`
		YearFrom    int      `json:"year_from"`
		YearTo      int      `json:"year_to"`
		Platforms   []int    `json:"platforms"`  // 类型多选：1=TV 2=电影 3=OVA 4=特别篇 5=Web；空=全部
		OnlyTV      bool     `json:"only_tv"`    // 兼容旧参数：等价于 platforms=[1]
		Countries   []string `json:"countries"`  // 国家/地区多选（Bangumi 标签名）；空=不限
		OnlyJapan   bool     `json:"only_japan"` // 兼容旧参数：等价于 countries=["日本"]
		FetchCovers bool     `json:"fetch_covers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		writeErr(w, 400, errors.New("归档目录不能为空"))
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		writeErr(w, 400, errors.New("归档目录不存在"))
		return
	}
	if req.YearTo == 0 {
		req.YearTo = time.Now().Year()
	}
	if req.YearFrom < 0 || req.YearTo < req.YearFrom {
		writeErr(w, 400, errors.New("年份范围无效"))
		return
	}
	for _, p := range req.Platforms {
		if p < 1 || p > 5 {
			writeErr(w, 400, errors.New("动画类型无效（1=TV 2=电影 3=OVA 4=特别篇 5=Web）"))
			return
		}
	}
	platforms := req.Platforms
	if len(platforms) == 0 && req.OnlyTV {
		platforms = []int{1} // 兼容旧版"仅 TV"勾选
	}
	countries := req.Countries
	if len(countries) == 0 && req.OnlyJapan {
		countries = []string{"日本"} // 兼容旧版"仅日本"勾选
	}
	var b *importer.Bangumi
	if req.FetchCovers {
		b = importer.NewBangumi(EffectiveBangumiBaseURL(a.Store, a.Cfg), EffectiveBangumiToken(a.Store, a.Cfg), a.Cfg.MediaDir)
	}
	if err := importer.StartArchiveImport(a.Store, b, importer.ArchiveOption{
		Dir: dir, YearFrom: req.YearFrom, YearTo: req.YearTo,
		Platforms: platforms, Countries: countries, FetchCovers: req.FetchCovers,
	}); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"started": true})
}

// ArchiveImportStatus GET /api/admin/anime/import-archive/status —— 前端轮询导入进度。
func (a *Admin) ArchiveImportStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, importer.ArchiveStatus())
}

// ArchiveLatestInfo GET /api/admin/anime/archive-latest
// 返回最新归档信息（GitHub latest.json）+ 本机缓存归档（容器部署时无法访问宿主目录，靠这个在线获取）。
func (a *Admin) ArchiveLatestInfo(w http.ResponseWriter, r *http.Request) {
	base := importer.ArchiveBaseDir(a.Cfg.DBPath)
	latest, err := importer.FetchArchiveLatest()
	resp := map[string]any{
		"latest":     latest,
		"latest_err": "",
		"cache":      importer.CacheArchiveInfo(a.Store, base),
	}
	if err != nil {
		resp["latest_err"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

// ArchiveDownload POST /api/admin/anime/archive-download {"force": false}
// 下载并解压最新归档到固定目录（DB 目录下 archive/，Docker 即数据卷）。
// 已有可用缓存且 force=false 时直接复用（不下载）。
func (a *Admin) ArchiveDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	json.NewDecoder(r.Body).Decode(&req) // 空体 = 复用/首次
	base := importer.ArchiveBaseDir(a.Cfg.DBPath)
	latest, err := importer.FetchArchiveLatest()
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	if err := importer.StartArchiveDownload(a.Store, base, latest, req.Force); err != nil {
		writeErr(w, 409, err)
		return
	}
	if !req.Force {
		if ci := importer.CacheArchiveInfo(a.Store, base); ci.Available {
			writeJSON(w, 200, map[string]any{"started": false, "reused": true, "dir": ci.Dir})
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"started": true})
}

// ArchiveDownloadStatus GET /api/admin/anime/archive-download/status —— 前端轮询下载进度。
func (a *Admin) ArchiveDownloadStatus(w http.ResponseWriter, r *http.Request) {
	base := importer.ArchiveBaseDir(a.Cfg.DBPath)
	writeJSON(w, 200, map[string]any{
		"download": importer.ArchiveDownloadStatus(),
		"cache":    importer.CacheArchiveInfo(a.Store, base),
	})
}

// FetchCovers POST /api/admin/anime/fetch-covers
// 对所有"有 bangumi_id 但缺封面"的番剧后台批量补拉封面（幂等，可反复执行）。
func (a *Admin) FetchCovers(w http.ResponseWriter, r *http.Request) {
	b := importer.NewBangumi(EffectiveBangumiBaseURL(a.Store, a.Cfg), EffectiveBangumiToken(a.Store, a.Cfg), a.Cfg.MediaDir)
	if err := importer.StartCoverFetch(a.Store, b); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"started": true})
}

// FetchCoversStatus GET /api/admin/anime/fetch-covers/status —— 前端轮询补封面进度。
func (a *Admin) FetchCoversStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, importer.CoverStatus())
}

// UploadMedia POST /api/admin/media —— multipart 字段 "file"（可多个）。
// 图片按内容哈希命名落地 media/，返回 ["media/xxxx.jpg", ...]。
func (a *Admin) UploadMedia(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		writeErr(w, 400, fmt.Errorf("解析上传文件: %w", err))
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeErr(w, 400, errors.New("缺少文件（multipart 字段名需为 file）"))
		return
	}
	dir, err := filepath.Abs(a.Cfg.MediaDir)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, err)
		return
	}
	var paths []string
	for _, fh := range files {
		if fh.Size > 20<<20 {
			continue
		}
		f, err := fh.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			continue
		}
		sum := sha1.Sum(data)
		name := hex.EncodeToString(sum[:]) + mediaExt(fh.Filename)
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			_ = os.WriteFile(p, data, 0o644)
		}
		paths = append(paths, "media/"+name)
	}
	if paths == nil {
		paths = []string{}
	}
	writeJSON(w, 200, map[string][]string{"paths": paths})
}

func mediaExt(name string) string {
	lower := strings.ToLower(name)
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
	case strings.HasSuffix(lower, ".jpeg"):
		return ".jpg"
	default:
		return ".jpg"
	}
}

// ImportSources GET /api/admin/importers —— 可用导入来源
func (a *Admin) ImportSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"sources": a.Importer.Names()})
}

// ---------- 字幕组源管理 ----------

type feedReq struct {
	Name     string `json:"name"`
	RSSURL   string `json:"rss_url"`
	BaseURL  string `json:"base_url"`
	AnimeID  *int64 `json:"anime_id"`
	Priority int    `json:"priority"`
	Enabled  *bool  `json:"enabled"`
	Mirror   *bool  `json:"mirror"` // 自动镜像资源文件开关
	Color    string `json:"color"`
}

// ListFeeds GET /api/admin/feeds
func (a *Admin) ListFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := a.Store.ListFeeds()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if feeds == nil {
		feeds = []*model.Feed{}
	}
	writeJSON(w, 200, feeds)
}

// CreateFeed POST /api/admin/feeds
func (a *Admin) CreateFeed(w http.ResponseWriter, r *http.Request) {
	var req feedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	// RSS 源必须隶属于一部番剧（优先级在番剧内独立生效）
	if req.AnimeID == nil || *req.AnimeID <= 0 {
		writeErr(w, 400, errors.New("anime_id 必填：RSS 源必须挂到具体番剧下"))
		return
	}
	if _, err := a.Store.QueryAnimeByID(*req.AnimeID); err != nil {
		writeErr(w, 404, errors.New("关联的番剧不存在"))
		return
	}
	enabled := 1
	if req.Enabled != nil {
		if *req.Enabled {
			enabled = 1
		} else {
			enabled = 0
		}
	}
	mirror := false
	if req.Mirror != nil {
		mirror = *req.Mirror
	}
	// site 从订阅地址反查真实域名（去 www，解析失败为空）；手动源无站点无关关键词
	id, err := a.Store.CreateFeed(req.Name, req.RSSURL, req.BaseURL, req.AnimeID, req.Priority, enabled, mirror, req.Color, disc.HostOf(req.RSSURL), "")
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	feed, _ := a.Store.GetFeed(id)
	writeJSON(w, 200, feed)
}

// UpdateFeed PUT /api/admin/feeds/{id}
func (a *Admin) UpdateFeed(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	feed, err := a.Store.GetFeed(id)
	if err != nil {
		writeErr(w, 404, errors.New("源不存在"))
		return
	}
	var req feedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	if req.Name != "" {
		feed.Name = req.Name
	}
	if req.RSSURL != "" {
		if feed.RSSURL != req.RSSURL {
			feed.Site = disc.HostOf(req.RSSURL) // 换地址后重新推导所属站点
		}
		feed.RSSURL = req.RSSURL
	}
	feed.BaseURL = req.BaseURL
	if req.Priority > 0 {
		feed.Priority = req.Priority
	}
	if req.Enabled != nil {
		feed.Enabled = *req.Enabled
	}
	if req.Mirror != nil {
		feed.Mirror = *req.Mirror
	}
	feed.Color = req.Color
	// anime_id：RSS 源始终隶属某部番剧，只允许换挂到另一部，不允许解除
	if req.AnimeID != nil && *req.AnimeID > 0 {
		v := *req.AnimeID
		feed.AnimeID = &v
	}
	if err := a.Store.UpdateFeed(feed); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, feed)
}

// DeleteFeed DELETE /api/admin/feeds/{id}
func (a *Admin) DeleteFeed(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	mirrorPaths, _ := a.Store.MirrorPathsForFeed(id)
	if err := a.Store.DeleteFeed(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	a.cleanupMirrorFiles(mirrorPaths)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// PurgeBlacklistedFeeds POST /api/admin/feeds/purge-blacklisted
// 批量清理：删除所有 名称/关键词 命中「自动添加·关键词黑名单」的 RSS 源
// （匹配语义与自动添加时 disc.BlackHit 完全一致：包含匹配、大小写不敏感），
// 连同其全部更新记录级联删除。用于"误导入了某个字幕组（如 Raw 组）要全库清掉"的场景。
// 依据当前已保存设置里的黑名单；黑名单为空 → 400。
func (a *Admin) PurgeBlacklistedFeeds(w http.ResponseWriter, r *http.Request) {
	black := EffectiveDiscBlackGroups(a.Store, a.Cfg)
	if len(black) == 0 {
		writeErr(w, 400, errors.New("黑名单为空：请先在设置里填写并保存「自动添加 · 关键词黑名单」"))
		return
	}
	feeds, err := a.Store.ListFeeds()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	type hitFeed struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		AnimeID *int64 `json:"anime_id"`
		Anime   string `json:"anime,omitempty"` // 番剧名（便于确认删的是谁的源）
	}
	var hits []hitFeed
	for _, fd := range feeds {
		if !disc.BlackHit(disc.Candidate{Group: fd.Name, Keyword: fd.Keyword}, black) {
			continue
		}
		h := hitFeed{ID: fd.ID, Name: fd.Name, AnimeID: fd.AnimeID}
		if fd.AnimeID != nil {
			if an, err := a.Store.QueryAnimeByID(*fd.AnimeID); err == nil {
				h.Anime = an.Title
			}
		}
		hits = append(hits, h)
	}
	if len(hits) == 0 {
		writeJSON(w, 200, map[string]any{"count": 0, "deleted": []hitFeed{}})
		return
	}
	deleted := 0
	var failed []string
	for _, h := range hits {
		mirrorPaths, _ := a.Store.MirrorPathsForFeed(h.ID)
		if err := a.Store.DeleteFeed(h.ID); err != nil {
			failed = append(failed, h.Name)
			log.Printf("[feed] 黑名单剔除源 %d (%s) 失败: %v", h.ID, h.Name, err)
			continue
		}
		a.cleanupMirrorFiles(mirrorPaths)
		deleted++
	}
	out := map[string]any{"count": deleted, "deleted": hits}
	if len(failed) > 0 {
		out["failed"] = failed
	}
	log.Printf("[feed] 黑名单剔除 RSS 源: 命中 %d 个，删除 %d 个%s", len(hits), deleted, pickFailedNote(failed))
	writeJSON(w, 200, out)
}

// pickFailedNote 部分失败时的日志后缀。
func pickFailedNote(failed []string) string {
	if len(failed) == 0 {
		return ""
	}
	return "，失败: " + strings.Join(failed, "、")
}

// FetchOne POST /api/admin/feeds/{id}/fetch
func (a *Admin) FetchOne(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	feed, err := a.Store.GetFeed(id)
	if err != nil {
		writeErr(w, 404, errors.New("源不存在"))
		return
	}
	// 抢不到锁（后台轮次/另一个手动任务在进行）→ 进入插队队列：
	// 持锁方跑完当前单元（当前源/当前镜像文件）后立即让位，本请求拿到锁继续执行
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer waitCancel()
	if !a.Fetcher.WaitBeginFetch(waitCtx) {
		writeErr(w, 409, errors.New("后台抓取持续未结束，手动刷新未能插队，请稍候再试"))
		return
	}
	defer a.Fetcher.EndFetch()
	// 用 Background 而非 r.Context()：关闭网页/断连不中断后台抓取，
	// 任务继续跑完并落库，前端重开页面轮询即可看到结果
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.Fetcher.FetchFeed(ctx, feed); err != nil {
		writeErr(w, 502, err)
		return
	}
	fresh, _ := a.Store.GetFeed(id)
	writeJSON(w, 200, fresh)
}

// FetchAll POST /api/admin/feeds/fetch-all
func (a *Admin) FetchAll(w http.ResponseWriter, r *http.Request) {
	// 与 FetchOne 相同：抢不到锁则插队等待（持锁方跑完当前单元后让位）
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer waitCancel()
	if !a.Fetcher.WaitBeginFetch(waitCtx) {
		writeErr(w, 409, errors.New("后台抓取持续未结束，手动刷新未能插队，请稍候再试"))
		return
	}
	defer a.Fetcher.EndFetch()
	// 用 Background 而非 r.Context()：关闭网页/断连不中断批量抓取，
	// 任务继续跑完并落库，前端重开页面即可看到结果
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	results := a.Fetcher.FetchAll(ctx)
	writeJSON(w, 200, results)
}

// ---------- 待归类 ----------

// Pending GET /api/admin/pending
func (a *Admin) Pending(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	list, err := a.Store.ListPending(limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.Release{}
	}
	writeJSON(w, 200, list)
}

type bindReq struct {
	AnimeID      int64 `json:"anime_id"`
	BindSiblings bool  `json:"bind_siblings"`
}

// BindRelease POST /api/admin/releases/{id}/bind
func (a *Admin) BindRelease(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	var req bindReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AnimeID <= 0 {
		writeErr(w, 400, errors.New("anime_id 必填"))
		return
	}
	if _, err := a.Store.QueryAnimeByID(req.AnimeID); err != nil {
		writeErr(w, 404, errors.New("目标番剧不存在"))
		return
	}
	if err := a.Store.BindRelease(id, req.AnimeID, req.BindSiblings); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- 静态托管（生产单端口部署，DESIGN §12） ----------

// StaticFrontend 若 FRONTEND_DIST 存在则托管编译产物，SPA 回退 index.html。
