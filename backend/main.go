package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"anidock/backend/config"
	"anidock/backend/internal/handler"
	"anidock/backend/internal/importer"
	"anidock/backend/internal/rss"
	"anidock/backend/internal/store"
)

// version 版本号：Docker 构建时经 -X main.version 注入（取自仓库根 VERSION 文件）；本地 go build 为 dev。
var version = "dev"

func main() {
	log.Printf("AniDock %s 启动", version)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置: %v", err)
	}

	st, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("初始化数据库: %v", err)
	}
	defer st.Close()

	if err := os.MkdirAll(cfg.MediaDir, 0o755); err != nil {
		log.Fatalf("创建 media 目录: %v", err)
	}
	if err := os.MkdirAll(cfg.MirrorDir, 0o755); err != nil {
		log.Fatalf("创建 mirror 目录: %v", err)
	}

	// 可插拔元数据导入器（DESIGN §5.2）
	// token/地址支持后台「设置」热改：TokenFunc/BaseFunc 每次请求时读取当前生效值
	bangumi := importer.NewBangumi(cfg.BangumiBaseURL, cfg.BangumiToken, cfg.MediaDir)
	bangumi.TokenFunc = func() string { return handler.EffectiveBangumiToken(st, cfg) }
	bangumi.BaseFunc = func() string { return handler.EffectiveBangumiBaseURL(st, cfg) }
	registry := importer.NewRegistry(
		bangumi,
		importer.NewLocalJSON(),
	)

	// 后台 RSS 轮询器（含资源文件自动镜像）
	fetcher := rss.NewFetcher(st, cfg.FetchInterval)
	fetcher.MirrorDir = cfg.MirrorDir
	fetcher.MaxMirrorBytes = cfg.MaxMirrorBytes
	// 轮询间隔/镜像上限/请求节流支持后台「设置」热改（运行时读取生效值）
	fetcher.IntervalFunc = func() time.Duration { return handler.EffectiveFetchInterval(st, cfg) }
	fetcher.MirrorMaxFunc = func() int64 { return handler.EffectiveMaxMirror(st, cfg) }
	fetcher.PaceFunc = func() time.Duration { return handler.EffectiveFetchPace(st, cfg) }
	// 站点池展开：带关键词的逻辑源抓取时按当前站点池逐站展开（每次读取生效设置，热改即时生效）
	fetcher.PoolURLs = func(kw string) []string {
		var out []string
		for _, s := range handler.EffectiveSitePoolList(st, cfg) {
			out = append(out, s.URL(kw))
		}
		return out
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fetcher.Start(ctx)

	media, _ := filepath.Abs(cfg.MediaDir)
	mirror, _ := filepath.Abs(cfg.MirrorDir)
	router := &handler.Router{
		Public: &handler.Public{Store: st},
		Admin:  handler.NewAdmin(st, registry, fetcher, cfg),
		Media:  handler.ServeMedia(media),
		Mirror: handler.ServeMirror(mirror),
	}
	// 开启访客认证且未认证时，HTML 入口只发门禁轻页面（不下发完整 SPA bundle）
	if h, ok := handler.StaticFrontend(cfg.FrontendDist, router.Admin.GateCheck); ok {
		router.Static = h
		log.Printf("前端静态托管: %s", cfg.FrontendDist)
	}

	r := router.Register()
	addr := ":" + cfg.Port
	log.Printf("服务启动: http://localhost%s (RSS 抓取间隔 %s)", addr, cfg.FetchInterval)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 90 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务异常退出: %v", err)
	}
	log.Println("服务已停止")
}
