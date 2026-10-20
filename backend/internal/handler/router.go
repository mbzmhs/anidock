package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

// Router 完整路由注册（DESIGN §9）。
type Router struct {
	Public *Public
	Admin  *Admin
	Media  http.HandlerFunc
	Mirror http.HandlerFunc // 可为 nil（未配置镜像目录时）
	Static http.Handler     // 可为 nil（开发期由 Vite 提供前端）
}

func (rt *Router) Register() chi.Router {
	r := chi.NewRouter()
	rc := cors.New(cors.Options{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}})
	r.Use(rc.Handler)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	r.Get("/media/{file}", rt.Media)
	if rt.Mirror != nil {
		r.Get("/mirror/{file}", rt.Mirror)
	}

	r.Route("/api", func(r chi.Router) {
		// 前台（只读；开启访客认证后需持访客/后台 token，见 RequireVisitor）
		r.Route("/", func(r chi.Router) {
			r.Use(rt.Admin.RequireVisitor)
			r.Get("/anime", rt.Public.ListAnime)
			r.Get("/anime/count", rt.Public.CountAnime)
			r.Get("/anime/{id}", rt.Public.GetAnime)
			r.Get("/anime/{id}/episodes", rt.Public.AnimeEpisodes)
			r.Get("/anime/{id}/feeds", rt.Public.AnimeFeeds)
			r.Get("/feeds", rt.Public.ListFeeds)
			r.Get("/latest", rt.Public.Latest)
		})

		// RSS 订阅流：唯一豁免访客门禁的前台接口——
		// 订阅地址是明文 URL 发给 RSS 阅读器的，阅读器无法携带 Authorization 头
		r.Get("/anime/{id}/rss", rt.Public.AnimeRSS)

		// 访客认证（防爬虫）：login 在门禁之外，套防爆破保护
		r.Post("/visitor/login", rt.Admin.LoginGuard("visitor/login", rt.Admin.VisitorLogin))

		// 首次初始化：仅在未配置任何后台口令时 /bootstrap 有效（同样防爆破）
		r.Get("/bootstrap/status", rt.Admin.BootstrapStatus)
		r.Post("/bootstrap", rt.Admin.LoginGuard("bootstrap", rt.Admin.Bootstrap))

		// 后台（鉴权）：login 在鉴权组之外，套防爆破保护；其余全部 RequireAdmin
		r.Post("/admin/login", rt.Admin.LoginGuard("admin/login", rt.Admin.Login))
		r.Route("/admin", func(r chi.Router) {
			r.Use(rt.Admin.RequireAdmin)
			r.Get("/anime", rt.Admin.ListAnime)
			r.Post("/anime", rt.Admin.CreateAnime)
			r.Put("/anime/{id}", rt.Admin.UpdateAnime)
			r.Delete("/anime/{id}", rt.Admin.DeleteAnime)
			r.Post("/anime/import", rt.Admin.Import)
			r.Post("/anime/preview", rt.Admin.PreviewAnime)
			r.Post("/anime/import-archive", rt.Admin.ImportArchive)
			r.Get("/anime/import-archive/status", rt.Admin.ArchiveImportStatus)
			r.Get("/anime/archive-latest", rt.Admin.ArchiveLatestInfo)
			r.Post("/anime/archive-download", rt.Admin.ArchiveDownload)
			r.Get("/anime/archive-download/status", rt.Admin.ArchiveDownloadStatus)
			r.Post("/anime/fetch-covers", rt.Admin.FetchCovers)
			r.Get("/anime/fetch-covers/status", rt.Admin.FetchCoversStatus)
			// RSS 自动发现（v0.2.0）：后台任务，结果轮询；apply 按候选批量建源
			r.Post("/anime/{id}/discover-rss", rt.Admin.DiscoverRss)
			r.Get("/anime/{id}/discover-rss", rt.Admin.DiscoveryStatus)
			r.Post("/anime/{id}/discover-rss/apply", rt.Admin.ApplyDiscovery)
			// RSS 批量发现（v0.3.0）：串行后台任务，可随时停止，关页不中断
			r.Post("/rss-bulk", rt.Admin.BulkDiscoverRss)
			r.Get("/rss-bulk", rt.Admin.BulkDiscoverStatus)
			r.Post("/rss-bulk/stop", rt.Admin.BulkDiscoverStop)
			r.Get("/settings", rt.Admin.GetSettings)
			r.Put("/settings", rt.Admin.SaveSettings)
			r.Post("/settings/test-bangumi", rt.Admin.TestBangumi)
			r.Post("/media", rt.Admin.UploadMedia)
			r.Get("/importers", rt.Admin.ImportSources)
			r.Get("/feeds", rt.Admin.ListFeeds)
			r.Post("/feeds", rt.Admin.CreateFeed)
			r.Put("/feeds/{id}", rt.Admin.UpdateFeed)
			r.Delete("/feeds/{id}", rt.Admin.DeleteFeed)
			r.Post("/feeds/{id}/fetch", rt.Admin.FetchOne)
			r.Post("/feeds/fetch-all", rt.Admin.FetchAll)
			r.Post("/feeds/purge-blacklisted", rt.Admin.PurgeBlacklistedFeeds)
			r.Get("/pending", rt.Admin.Pending)
			r.Post("/releases/{id}/bind", rt.Admin.BindRelease)
		})
	})

	// 生产：单端口托管编译后的前端（SPA 回退）
	if rt.Static != nil {
		srv := rt.Static
		r.NotFound(func(w http.ResponseWriter, req *http.Request) { srv.ServeHTTP(w, req) })
	}
	return r
}
