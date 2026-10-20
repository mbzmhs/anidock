package store

// 运行时设置（settings 表）：后台设置模态窗维护，覆盖 .env 中的同项配置；
// 留空/删除即回落到 .env 值，全部热生效（无需重启）。

const (
	SettingBangumiToken    = "bangumi_token"     // Bangumi API token
	SettingBangumiBaseURL  = "bangumi_base_url"  // Bangumi API 地址
	SettingAdminPassword   = "admin_password"    // 后台口令（覆盖 .env ADMIN_PASSWORD）
	SettingVisitorPassword = "visitor_password"  // 访客口令（空 = 不启用访客认证，站点公开）
	SettingTokenTTL        = "token_ttl"         // 登录 token 有效期：后台+访客共用；"0" = 无限制，否则 Go duration（如 720h）
	SettingFetchInterval   = "fetch_interval"    // RSS 轮询间隔（Go duration，如 3m）
	SettingFetchPace       = "fetch_pace"        // 单次订阅请求节流间隔（Go duration，如 2s；实际等待 = 间隔 + 随机 0..间隔；0 = 关闭节流）
	SettingDiscMaxPicks    = "disc_max_picks"    // 发现后自动添加的最多选择数量（默认 2）
	SettingDiscMinPicks    = "disc_min_picks"    // 发现后自动添加的最少选择数量（默认 1，不超过最多数量）
	SettingDiscPrefGroups  = "disc_pref_groups"  // 自动添加偏好字幕组/关键词（空格/换行分隔；越靠前优先级越高，命中的候选优先入选）
	SettingDiscBlackGroups = "disc_black_groups" // 自动添加关键词黑名单（空格/换行分隔；组名/关键词命中的候选不被自动选中）
	SettingMirrorMaxSize   = "mirror_max_size"   // 单文件镜像上限（如 4GiB 或字节数）
	SettingSitePool        = "site_pool"         // 订阅站点池（逗号/换行分隔的 URL 模板（{} = 关键词）或站点地址；默认空 = 关闭自动 RSS）
)

// GetSetting 读取设置项；ok=false 表示未设置（应回落到 .env / 默认值）。
func (s *Store) GetSetting(key string) (string, bool) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// SetSetting 写入设置项。
func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

// DeleteSetting 删除设置项（回落到 .env）。
func (s *Store) DeleteSetting(key string) error {
	_, err := s.DB.Exec("DELETE FROM settings WHERE key=?", key)
	return err
}
