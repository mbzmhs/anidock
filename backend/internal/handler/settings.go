package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"anidock/backend/config"
	"anidock/backend/internal/importer"
	"anidock/backend/internal/rss/disc"
	"anidock/backend/internal/store"
)

// DefaultSitePool 订阅站点池默认值：空 = 关闭自动 RSS 抓取
// （自动发现/建源不使用任何站点）；由用户在设置页配置站点地址列表。
const DefaultSitePool = ""

// EffectiveSitePoolList 当前生效的站点池：用户配置的 URL 模板/站点地址列表（按行/逗号分隔，
// 按真实域名（去 www）去重）；默认空 = 自动 RSS 抓取关闭。
// 解析逻辑与抓取器共用（disc.PoolSites）。
func EffectiveSitePoolList(st *store.Store, cfg *config.Config) []disc.Site {
	raw := settingOr(st, store.SettingSitePool, DefaultSitePool)
	return disc.PoolSites(raw)
}

// ---------- 生效配置：settings 表（后台设置页热改）覆盖 .env ----------

func settingOr(st *store.Store, key, fallback string) string {
	if v, ok := st.GetSetting(key); ok && v != "" {
		return v
	}
	return fallback
}

// settingSource "db" = 后台设置页覆盖过；"env" = 用 .env / 默认值。
func settingSource(st *store.Store, key, fallback string) string {
	if v, ok := st.GetSetting(key); ok && v != "" {
		return "db"
	}
	return "env"
}

func EffectiveBangumiToken(st *store.Store, cfg *config.Config) string {
	return settingOr(st, store.SettingBangumiToken, cfg.BangumiToken)
}

func EffectiveBangumiBaseURL(st *store.Store, cfg *config.Config) string {
	return settingOr(st, store.SettingBangumiBaseURL, cfg.BangumiBaseURL)
}

func EffectiveAdminPassword(st *store.Store, cfg *config.Config) string {
	return settingOr(st, store.SettingAdminPassword, cfg.AdminPassword)
}

func EffectiveVisitorPassword(st *store.Store, cfg *config.Config) string {
	return settingOr(st, store.SettingVisitorPassword, cfg.VisitorPassword)
}

// EffectiveTokenTTL 登录 token 有效期；<=0 = 无限制（签发永久 token）。
func EffectiveTokenTTL(st *store.Store, cfg *config.Config) time.Duration {
	v := settingOr(st, store.SettingTokenTTL, "")
	if v == "" {
		return 24 * time.Hour // 默认 24h（与旧行为一致）
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return d
}

func EffectiveFetchInterval(st *store.Store, cfg *config.Config) time.Duration {
	v := settingOr(st, store.SettingFetchInterval, "")
	if v == "" {
		return cfg.FetchInterval
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return cfg.FetchInterval
	}
	return d
}

// DefaultFetchPace 单请求节流间隔默认值（实际等待 = 间隔 + 随机 0..间隔）。
const DefaultFetchPace = 2 * time.Second

// EffectiveFetchPace 当前生效的单请求节流间隔（0 = 关闭节流）。
func EffectiveFetchPace(st *store.Store, cfg *config.Config) time.Duration {
	v := settingOr(st, store.SettingFetchPace, "")
	if v == "" {
		return DefaultFetchPace
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return DefaultFetchPace
	}
	return d
}

// paceDisplay 当前生效的节流间隔（原样字符串；未设置 = 默认值）。
func paceDisplay(st *store.Store) string {
	if v, ok := st.GetSetting(store.SettingFetchPace); ok && v != "" {
		return v
	}
	return DefaultFetchPace.String()
}

// 自动添加组合逻辑默认值：最多 2 个、最少 1 个。
const (
	DefaultDiscMaxPicks = 2
	DefaultDiscMinPicks = 1
)

func settingInt(st *store.Store, key string, fallback int) int {
	v := settingOr(st, key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

// EffectiveDiscMaxPicks 发现后自动添加的最多选择数量（未设置/非法 = 默认 2）。
func EffectiveDiscMaxPicks(st *store.Store, cfg *config.Config) int {
	return settingInt(st, store.SettingDiscMaxPicks, DefaultDiscMaxPicks)
}

// EffectiveDiscMinPicks 发现后自动添加的最少选择数量（未设置/非法 = 默认 1）。
func EffectiveDiscMinPicks(st *store.Store, cfg *config.Config) int {
	return settingInt(st, store.SettingDiscMinPicks, DefaultDiscMinPicks)
}

// EffectiveDiscPrefGroups 自动添加偏好字幕组/关键词（保持输入顺序；
// 空格/换行/制表符皆可作分隔符；未设置 = 空 = 纯兜底排序）。
func EffectiveDiscPrefGroups(st *store.Store, cfg *config.Config) []string {
	v := settingOr(st, store.SettingDiscPrefGroups, "")
	return strings.Fields(v)
}

// EffectiveDiscBlackGroups 自动添加关键词黑名单（空格/换行分隔；
// 组名/关键词命中的候选直接排除在自动选择之外；未设置 = 不排除）。
func EffectiveDiscBlackGroups(st *store.Store, cfg *config.Config) []string {
	v := settingOr(st, store.SettingDiscBlackGroups, "")
	return strings.Fields(v)
}

// discSettingDisplay 自动添加设置的原样展示（未设置 = 默认值/空）。
func discSettingDisplay(st *store.Store, key string, fallback string) string {
	if v, ok := st.GetSetting(key); ok && v != "" {
		return v
	}
	return fallback
}

func EffectiveMaxMirror(st *store.Store, cfg *config.Config) int64 {
	v := settingOr(st, store.SettingMirrorMaxSize, "")
	if v == "" {
		return cfg.MaxMirrorBytes
	}
	n, err := config.ParseSize(v)
	if err != nil || n <= 0 {
		return cfg.MaxMirrorBytes
	}
	return n
}

// ttlDisplay 当前生效的 token 有效期（原样字符串："0"=无限制，否则 duration）。
func ttlDisplay(st *store.Store, cfg *config.Config) string {
	if v, ok := st.GetSetting(store.SettingTokenTTL); ok && v != "" {
		return v
	}
	d := EffectiveTokenTTL(st, cfg)
	if d <= 0 {
		return "0"
	}
	return d.String()
}

// maskSecret 脱敏展示长密钥/口令。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 6 {
		return "••••"
	}
	return s[:2] + "••••" + s[len(s)-2:]
}

// ---------- 设置读写 ----------

// GetSettings GET /api/admin/settings —— 当前生效配置（密钥脱敏）+ 只读信息。
func (a *Admin) GetSettings(w http.ResponseWriter, r *http.Request) {
	st, cfg := a.Store, a.Cfg
	writeJSON(w, 200, map[string]any{
		"bangumi_base_url":     settingOr(st, store.SettingBangumiBaseURL, cfg.BangumiBaseURL),
		"bangumi_base_source":  settingSource(st, store.SettingBangumiBaseURL, cfg.BangumiBaseURL),
		"bangumi_token_masked": maskSecret(EffectiveBangumiToken(st, cfg)),
		"bangumi_token_source": settingSource(st, store.SettingBangumiToken, cfg.BangumiToken),
		"admin_pw_set":         EffectiveAdminPassword(st, cfg) != "",
		"admin_pw_source":      settingSource(st, store.SettingAdminPassword, cfg.AdminPassword),
		"visitor_enabled":      EffectiveVisitorPassword(st, cfg) != "",
		"visitor_pw_source":    settingSource(st, store.SettingVisitorPassword, cfg.VisitorPassword),
		"token_ttl":            ttlDisplay(st, cfg),
		"fetch_interval":       settingOr(st, store.SettingFetchInterval, cfg.FetchInterval.String()),
		"fetch_pace":           paceDisplay(st),
		"disc_max_picks":       discSettingDisplay(st, store.SettingDiscMaxPicks, strconv.Itoa(DefaultDiscMaxPicks)),
		"disc_min_picks":       discSettingDisplay(st, store.SettingDiscMinPicks, strconv.Itoa(DefaultDiscMinPicks)),
		"disc_pref_groups":     discSettingDisplay(st, store.SettingDiscPrefGroups, ""),
		"disc_black_groups":    discSettingDisplay(st, store.SettingDiscBlackGroups, ""),
		"mirror_max_size":      settingOr(st, store.SettingMirrorMaxSize, sizeDisplay(cfg.MaxMirrorBytes)),
		"site_pool":            settingOr(st, store.SettingSitePool, DefaultSitePool),
		"site_pool_source":     settingSource(st, store.SettingSitePool, DefaultSitePool),
		"readonly": map[string]string{
			"port":   cfg.Port,
			"db":     cfg.DBPath,
			"media":  cfg.MediaDir,
			"mirror": cfg.MirrorDir,
		},
	})
}

// sizeDisplay 字节数转人类可读（4GiB / 512MiB）。
func sizeDisplay(n int64) string {
	const gib = int64(1) << 30
	const mib = int64(1) << 20
	switch {
	case n >= gib && n%gib == 0:
		return fmt.Sprintf("%dGiB", n/gib)
	case n >= mib && n%mib == 0:
		return fmt.Sprintf("%dMiB", n/mib)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// SaveSettings PUT /api/admin/settings
// body 为 map：只更新提供的键；空串 = 删除覆盖（回落到 .env）。
func (a *Admin) SaveSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	st := a.Store

	setOrDel := func(key, v string) error {
		if v == "" {
			return st.DeleteSetting(key)
		}
		return st.SetSetting(key, v)
	}

	for key, v := range req {
		v = strings.TrimSpace(v)
		switch key {
		case store.SettingBangumiToken:
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingBangumiBaseURL:
			if v != "" && !strings.HasPrefix(v, "http") {
				writeErr(w, 400, errors.New("Bangumi API 地址需以 http(s) 开头"))
				return
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingAdminPassword:
			// 空串 = 移除覆盖，回落到 .env 的 ADMIN_PASSWORD
			if v != "" && len(v) < 6 {
				writeErr(w, 400, errors.New("后台口令至少 6 位"))
				return
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingVisitorPassword:
			// 空串 = 关闭访客认证（站点恢复公开访问）
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingTokenTTL:
			if v != "" && v != "0" {
				if d, err := time.ParseDuration(v); err != nil || d <= 0 {
					writeErr(w, 400, fmt.Errorf("token 有效期格式错误（0=无限制，否则如 24h/168h/720h）: %v", err))
					return
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingFetchInterval:
			if v != "" {
				if d, err := time.ParseDuration(v); err != nil || d < time.Minute {
					writeErr(w, 400, fmt.Errorf("轮询间隔非法（至少 1 分钟，如 3m/30m/2h）: %v", err))
					return
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingFetchPace:
			if v != "" && v != "0" {
				if d, err := time.ParseDuration(v); err != nil || d < 0 {
					writeErr(w, 400, fmt.Errorf("请求节流间隔格式错误（0=关闭，否则如 1s/2s/5s）: %v", err))
					return
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingMirrorMaxSize:
			if v != "" {
				if n, err := config.ParseSize(v); err != nil || n < 1<<20 {
					writeErr(w, 400, fmt.Errorf("镜像大小上限非法（至少 1MiB，如 512MiB/4GiB）: %v", err))
					return
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingSitePool:
			// 空串 = 关闭自动 RSS 抓取；每个条目必须是 URL 模板（{} = 关键词）或 http/https 站点地址
			if v != "" {
				for _, part := range disc.SplitPool(v) {
					if part == "" {
						continue
					}
					if disc.SiteFromEntry(part) == nil {
						writeErr(w, 400, fmt.Errorf("站点条目 %q 非法（需为 http/https 的 URL 模板（{} = 关键词占位符）或站点地址，一行一个）", part))
						return
					}
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingDiscMaxPicks:
			if v != "" {
				if n, err := strconv.Atoi(v); err != nil || n < 1 {
					writeErr(w, 400, fmt.Errorf("自动添加最多选择数量非法（正整数，默认 %d）", DefaultDiscMaxPicks))
					return
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingDiscMinPicks:
			if v != "" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 {
					writeErr(w, 400, fmt.Errorf("自动添加最少选择数量非法（正整数，默认 %d）", DefaultDiscMinPicks))
					return
				}
				// 交叉校验：本次请求同时改了 max 时按新值比，否则按当前生效值比
				maxNow := EffectiveDiscMaxPicks(st, a.Cfg)
				if nv, ok := req[store.SettingDiscMaxPicks]; ok {
					if m, err := strconv.Atoi(strings.TrimSpace(nv)); err == nil && m > 0 {
						maxNow = m
					}
				}
				if n > maxNow {
					writeErr(w, 400, fmt.Errorf("最少选择数量（%d）不能大于最多选择数量（%d）", n, maxNow))
					return
				}
			}
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		case store.SettingDiscPrefGroups, store.SettingDiscBlackGroups:
			// 自由文本（空格/换行分隔的关键词；偏好词顺序即优先级，黑名单无顺序语义），无需校验
			if err := setOrDel(key, v); err != nil {
				writeErr(w, 500, err)
				return
			}
		default:
			writeErr(w, 400, fmt.Errorf("未知设置项: %s", key))
			return
		}
	}
	a.GetSettings(w, r)
}

// TestBangumi POST /api/admin/settings/test-bangumi —— 用当前生效的地址/token 实测连通性。
func (a *Admin) TestBangumi(w http.ResponseWriter, r *http.Request) {
	b := importer.NewBangumi(
		EffectiveBangumiBaseURL(a.Store, a.Cfg),
		EffectiveBangumiToken(a.Store, a.Cfg),
		a.Cfg.MediaDir,
	)
	if err := b.Ping(); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 访客认证（防爬虫） ----------
// 开启访客口令后，前台数据接口需持有效访客 token（或后台 token）访问；
// 未开启时全部放行，站点保持公开。

// VisitorLogin POST /api/visitor/login
func (a *Admin) VisitorLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	pw := EffectiveVisitorPassword(a.Store, a.Cfg)
	if pw == "" {
		writeErr(w, 400, errors.New("访客认证未开启"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(pw)) != 1 {
		writeErr(w, 401, errors.New("口令错误"))
		return
	}
	token, permanent, err := a.visitors.issue()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// 镜像 cookie：仅用于服务端 HTML 分流（门禁页 or 完整 SPA），httpOnly
	a.setVisitorCookie(w, token, permanent)
	ttl := "无限制"
	if !permanent {
		ttl = a.tokenTTLFunc().String()
	}
	writeJSON(w, 200, map[string]string{"token": token, "expires_in": ttl})
}

// VisitorAuthRequired 未通过访客认证时返回的哨兵错误文案（前端据此跳转访客登录页）。
const VisitorAuthRequired = "visitor-auth-required"

// RequireVisitor 前台接口门禁：未开启访客认证 → 直接放行；
// 开启后需持有效访客 token 或后台 token。
func (a *Admin) RequireVisitor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if EffectiveVisitorPassword(a.Store, a.Cfg) == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != "" && token != auth && (a.visitors.valid(token) || a.tokens.valid(token)) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, 401, errors.New(VisitorAuthRequired))
	})
}
