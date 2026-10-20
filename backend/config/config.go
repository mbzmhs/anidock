package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 全局配置（来自 .env / 环境变量，见 .env.example）
type Config struct {
	BangumiBaseURL string
	BangumiToken   string
	DBPath         string
	MediaDir       string
	MirrorDir      string // 资源文件自动镜像目录
	MaxMirrorBytes int64  // 单文件镜像大小上限
	FetchInterval  time.Duration
	Port           string
	AdminPassword  string
	VisitorPassword string // 访客口令（防爬虫；空 = 站点公开）。可被 settings 表覆盖
	FrontendDist   string
}

// Load 读取配置。若存在 .env 文件则先加载其中变量（不覆盖已存在的环境变量）。
func Load() (*Config, error) {
	loadDotEnv(".env")

	cfg := &Config{
		BangumiBaseURL: envOr("BANGUMI_BASE_URL", "https://api.bgm.tv"),
		BangumiToken:   os.Getenv("BANGUMI_TOKEN"),
		DBPath:         envOr("DB_PATH", "./data/db.sqlite"),
		MediaDir:       envOr("MEDIA_DIR", "./media"),
		MirrorDir:      envOr("MIRROR_DIR", "./data/mirror"),
		Port:           envOr("PORT", "8080"),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
		VisitorPassword: os.Getenv("VISITOR_PASSWORD"),
		FrontendDist:   envOr("FRONTEND_DIST", "../frontend/dist"),
		FetchInterval:  3 * time.Minute,
	}
	if iv := os.Getenv("FETCH_INTERVAL"); iv != "" {
		d, err := time.ParseDuration(iv)
		if err != nil {
			return nil, fmt.Errorf("FETCH_INTERVAL 非法（需要如 3m/30s 的 Go duration）: %v", err)
		}
		cfg.FetchInterval = d
	}
	// 镜像大小上限：默认 1MiB（只镜像种子文件，正常远小于此）；支持 MIRROR_MAX_SIZE（字节数，或如 "2GiB"/"512MiB"）
	cfg.MaxMirrorBytes = 1 << 20
	if v := os.Getenv("MIRROR_MAX_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxMirrorBytes = n
		} else if n, err := parseSize(v); err == nil && n > 0 {
			cfg.MaxMirrorBytes = n
		}
	}
	return cfg, nil
}

// ParseSize 解析 "512MiB" / "2GiB" / "100MB" 之类的大小写法（也接受纯字节数）。
func ParseSize(s string) (int64, error) {
	return parseSize(s)
}

// parseSize 解析 "512MiB" / "2GiB" / "100MB" 之类的大小写法。
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("空大小")
	}
	units := []struct{ suf string; mult int64}{
		{"GiB", 1 << 30}, {"Gi", 1 << 30}, {"GB", 1 << 30},
		{"MiB", 1 << 20}, {"Mi", 1 << 20}, {"MB", 1 << 20},
		{"KiB", 1 << 10}, {"Ki", 1 << 10}, {"KB", 1 << 10},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(s, u.suf)), 10, 64)
			if err != nil {
				return 0, err
			}
			return n * u.mult, nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("MIRROR_MAX_SIZE 非法: %v", err)
	}
	return n, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// loadDotEnv 极简 .env 解析：KEY=VALUE，# 开头为注释。不覆盖已存在的变量。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := trim(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		key, val, ok := cutKey(line)
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}

func cutKey(line string) (string, string, bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == '=' {
			return trim(line[:i]), trim(line[i+1:]), true
		}
	}
	return "", "", false
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '"') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '"' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
