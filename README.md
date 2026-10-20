# AniDock · 番港

> 各字幕组的番剧更新像船只进港，聚合到一个站点。

[![License](https://img.shields.io/badge/license-MIT-blue)](./LICENSE)
[![Version](https://img.shields.io/badge/version-0.2.4-blue)](./VERSION)
[![Platform](https://img.shields.io/badge/platform-Docker%20%7C%20Linux%20%7C%20macOS%20%7C%20Windows-lightgrey)](#-快速开始)
[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8)](https://go.dev)
[![Vue](https://img.shields.io/badge/Vue-3-42b883)](https://vuejs.org)

AniDock 是一个**自托管**的字幕组 RSS 番剧聚合站。把多个字幕组的 RSS 接入进来，后台自动抓取、解析标题里的集数，按优先级去重——**每一集只展示一个最优资源**，让你在一个页面里追完一部番剧的所有可用版本。番剧的封面、简介、角色、声优等静态信息来自 [Bangumi 官方归档](https://github.com/bangumi/Archive)，运行时**不依赖任何实时接口**，离线也能跑。

**技术栈**：Go (chi) · SQLite (纯 Go 无 CGO) · Vue 3 · Vite · Naive UI

## ✨ 特性

- **多字幕组聚合** — 每个字幕组一个 RSS 源，可设优先级、启停、立即抓取。
- **自动解析 + 匹配** — 从标题自动识别集数、画质、组名，并匹配到番剧；没匹配上的进「待归类」，一键绑定。
- **每集去重** — 同一集多个源都发布了，只展示优先级最高的，其余折叠在「其它源」。
- **自动发现** — 输入番剧名，自动在站点池里发现该番剧各字幕组的 RSS 源并批量导入（单发 / 批量）。
- **角色与声优** — 从 Bangumi 官方归档批量导入番剧，附带角色表与声优。
- **种子文件镜像** — 只把直链里的 `.torrent` 种子文件镜像到本地（防链接失效），视频等非种子资源一律不收，详情页可直接下载（magnet 除外）。
- **访客门禁** — 可选设置访客口令保护站点不被公开爬取；RSS 订阅流始终开放给阅读器。
- **单端口部署** — Go 单二进制托管前端 + API，一个端口跑起来，Docker 一键部署。

## 🚀 快速开始

### Docker（推荐）

单镜像（前端产物 + Go 二进制，约 30MB）：

```bash
# 构建：版本号取自仓库根 VERSION 文件，同时打 版本标签 + latest
docker build \
  --build-arg VERSION=$(cat VERSION) \
  -t anidock:v$(cat VERSION) -t anidock:latest .

# 运行：数据目录单独挂载，升级镜像不丢数据
docker run -d --name anidock --restart unless-stopped \
  -p 8080:8080 \
  -v anidock-data:/app/data \
  -v anidock-media:/app/media \
  anidock:latest
```

国内网络若拉不动 Go 模块，构建时追加 `--build-arg GOPROXY=https://goproxy.cn,direct`。

### 本地开发

```bash
# 后端（:8080，含后台 RSS 轮询）
cp .env.example backend/.env   # 填入 ADMIN_PASSWORD 等
cd backend && go run .

# 前端开发模式（:5173，/api 代理到 :8080）
cd frontend && npm install && npm run dev
```

生产单端口：`cd frontend && npm run build` 后启动后端，访问 `:8080`（后端自动托管 `frontend/dist`）。

## 🚦 首次使用

1. **初始化** — 若未配置 `ADMIN_PASSWORD`，首次打开会进入「初始化系统」页，设置后台口令（必选）与访客口令（可选）。
2. **导入番剧** — 后台「番剧管理 → 归档导入」，从 Bangumi 官方归档按年份/类型/国家批量导入（附带角色与声优）。
3. **接入字幕组** — 添加 RSS 源并设优先级（数字越小越优先）。
4. **自动轮询** — 之后后台按 `FETCH_INTERVAL`（默认 3 分钟）自动抓取，漏匹配的到「待归类」一键绑定。

## ⚙️ 配置

环境变量（`backend/.env`，复制 `.env.example` 填写）：

| 变量 | 说明 | 默认 |
|---|---|---|
| `BANGUMI_TOKEN` | Bangumi token（封面抓取用） | |
| `DB_PATH` | SQLite 数据库路径 | `./data/db.sqlite` |
| `MEDIA_DIR` | 图片落地目录 | `./media` |
| `MIRROR_DIR` | 种子镜像目录 | `./data/mirror` |
| `MIRROR_MAX_SIZE` | 单文件镜像上限（如 `512MiB`） | `1MiB` |
| `FETCH_INTERVAL` | RSS 轮询间隔（Go duration） | `3m` |
| `PORT` | 监听端口 | `8080` |
| `ADMIN_PASSWORD` | 后台口令（首次登录用，之后可在设置里改） | |
| `VISITOR_PASSWORD` | 访客口令（留空 = 公开站点） | |

后台「⚙ 设置」可覆盖上述配置并**热生效**，无需重启；留空即删除覆盖、回落 .env。

## 📁 目录结构

```
anidock/
├─ backend/          # Go 后端（抓取 / 解析 / 匹配 / 导入 / 路由 / 存储）
│  └─ testdata/      # 开发辅助：mock 字幕组源 + 冒烟脚本
├─ frontend/         # Vue 3 前端（前台 + 后台）
├─ .env.example      # 环境变量模板
├─ Dockerfile        # 单镜像构建
├─ docker-compose.yml
└─ VERSION           # 版本号（打进二进制与镜像标签）
```

## 🤝 贡献

欢迎 Issue 与 PR。本地跑通后端单测：`cd backend && go test ./...`。

## 📄 License

[MIT](./LICENSE)

## ⚖️ 免责声明

本项目仅聚合公开的字幕组 RSS 源，不存储、分发任何影视资源本体。所有内容版权均归原作者与字幕组所有，请支持正版、合理使用。
