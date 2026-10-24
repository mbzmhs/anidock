-- 字幕组 RSS 番剧聚合站 · SQLite schema
-- anime(静态) 与 release(动态) 彻底分层，见 DESIGN.md §6/§15.5

-- 番剧（静态元数据快照；图片已下载并按 GUID 命名）
CREATE TABLE IF NOT EXISTS anime (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  bangumi_id   INTEGER UNIQUE,
  title        TEXT NOT NULL,
  title_orig   TEXT,
  synopsis     TEXT,
  year         INTEGER,
  quarter      INTEGER,
  season_label TEXT,
  genres       TEXT,   -- JSON 数组
  cover        TEXT,
  poster       TEXT,
  images       TEXT,   -- JSON 数组
  aliases      TEXT,   -- JSON 数组：RSS 匹配别名
  studio       TEXT,   -- 动画制作公司
  info         TEXT,   -- JSON 数组：Bangumi infobox 制作信息（公司/人员/平台…）
  characters   TEXT,   -- JSON 数组：角色名
  source       TEXT,
  imported_at  TEXT,
  updated_at   TEXT
);
CREATE INDEX IF NOT EXISTS idx_anime_year_q ON anime(year, quarter);

-- 番剧角色/声优（Bangumi Archive 本地关联：subject-characters × character × person-characters × person）
-- v0 API 已不再提供角色数据，归档导入是唯一来源；重跑导入幂等回填
CREATE TABLE IF NOT EXISTS anime_cast (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  anime_id INTEGER NOT NULL,
  name     TEXT NOT NULL,   -- 角色名
  lead     INTEGER NOT NULL DEFAULT 0,  -- 角色定位：1=主角 2=配角 3=客串（其余=未标注）
  seiyuu   TEXT,            -- JSON 数组：该角色的声优（含版本标识，可空）
  sort     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_anime_cast_anime ON anime_cast(anime_id);

-- 后台登录 token（持久化：进程重启后未过期的 token 仍有效，浏览器端 localStorage 里的 token 不用重登）
-- permanent=1 表示永不过期（设置里过期时长选"无限制"）
CREATE TABLE IF NOT EXISTS admin_token (
  token      TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL,
  permanent  INTEGER NOT NULL DEFAULT 0
);

-- 运行时设置（后台「设置」模态窗维护；覆盖 .env，留空即回落到 .env 值）
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 访客认证 token（开启访客密码后，前台页面/接口需持有效 token 访问，防爬虫）
CREATE TABLE IF NOT EXISTS visitor_token (
  token      TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL,
  permanent  INTEGER NOT NULL DEFAULT 0
);

-- 字幕组 RSS 源（聚合对象，带优先级；feed 粒度 = 制作组 × 番剧 的 per-show 源，见 DESIGN §15.2）
-- site = 订阅地址所属站点（真实域名，去 www；手动源可能为空）；
-- keyword = 站点无关的订阅关键词（自动发现产出；兄弟站同关键词各自物化 URL 做镜像冗余，同组同集经 (anime,episode,group) 唯一索引自动合行）
CREATE TABLE IF NOT EXISTS feed (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT NOT NULL,
  rss_url         TEXT NOT NULL,
  base_url        TEXT,
  anime_id        INTEGER,               -- 可空：feed 关联到指定番剧（per-show 源自动/手动绑定）
  priority        INTEGER NOT NULL DEFAULT 100,  -- 越小越优先
  enabled         INTEGER NOT NULL DEFAULT 1,
  mirror          INTEGER NOT NULL DEFAULT 0,  -- 1 = 新资源文件自动下载镜像到本地
  site            TEXT,                  -- 订阅地址所属站点（v0.2.0 多站点冗余）
  keyword         TEXT,                  -- 站点无关订阅关键词（兄弟站可据此物化/自愈）
  color           TEXT,
  last_fetched_at TEXT,
  last_error      TEXT,
  created_at      TEXT
);

-- 番剧-源 关联（辅助 RSS 模糊匹配兜底）
CREATE TABLE IF NOT EXISTS anime_feed (
  anime_id INTEGER NOT NULL,
  feed_id  INTEGER NOT NULL,
  PRIMARY KEY (anime_id, feed_id)
);

-- 资源条目（多源都入库，展示时按优先级"每集取最优"去重）
CREATE TABLE IF NOT EXISTS release (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  feed_id       INTEGER NOT NULL,
  anime_id      INTEGER,                  -- 未匹配则空（进"待归类"）
  title         TEXT NOT NULL,            -- RSS 原始标题
  episode       REAL,
  episode_to    REAL,                     -- 合集区间终点（非空 = 多文件合集，episode 为起点）
  url           TEXT,                     -- 详情页链接（主操作「打开详情」）
  download_url  TEXT,                     -- 磁力/种子（可空）
  quality       TEXT,                     -- 解析出的画质（1080p/4K/...）
  spec          TEXT,                     -- 完整规格词（如 "4K HEVC"）
  group_name    TEXT,                     -- 解析出的制作组名
  published_at  TEXT,
  fetched_at    TEXT,
  dedup         TEXT NOT NULL,            -- 去重键：url 优先，无 url 时用标题+组名哈希
  mirror_path   TEXT,                     -- 本地镜像文件名（校验通过+原子落盘后才有值）
  mirror_size   INTEGER NOT NULL DEFAULT 0 -- 镜像文件大小（字节）
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_release_feed_dedup ON release(feed_id, dedup);
-- 合集区间进唯一键（单集 episode_to 为 NULL 归一为 -1）：避免"01-12 合集"与
-- 真实第 1 集在同组同键上互相覆盖。索引在 migrate() 里创建（存量库需先补列再建）。
CREATE INDEX IF NOT EXISTS idx_release_anime_ep ON release(anime_id, episode);
CREATE INDEX IF NOT EXISTS idx_release_pub ON release(published_at);

-- RSS 自动发现任务（v0.2.0）：一次发现 = 一行；后台任务跑完把报告 JSON 落库，
-- 前端轮询本表即可（关页不中断）。每部番剧保留最近 20 条记录。
CREATE TABLE IF NOT EXISTS rss_discovery (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  anime_id    INTEGER NOT NULL,
  site        TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'running',  -- running / done / error
  phase       TEXT,                             -- 进行中阶段（宽搜/种子扩展/候选回验）
  scanned     INTEGER NOT NULL DEFAULT 0,       -- 去重后扫描条目数
  matched     INTEGER NOT NULL DEFAULT 0,       -- 归属命中条目数
  queries     TEXT,                             -- JSON 数组：实际使用的宽搜关键词
  candidates  TEXT,                             -- JSON 数组：流派候选（含纯净订阅地址）
  unmatched   TEXT,                             -- JSON 数组：低置信条目（人工确认区）
  notes       TEXT,                             -- JSON 数组：警告/说明
  error       TEXT,
  started_at  TEXT,
  finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_rss_discovery_anime ON rss_discovery(anime_id, id);

-- RSS 批量发现任务（v0.3.0）：一次批量任务 = 一行；串行执行，
-- 进度（当前番剧/阶段/逐剧结果）落库，可随时停止；仅保留最近 20 条。
CREATE TABLE IF NOT EXISTS rss_bulk_discovery (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  status           TEXT NOT NULL DEFAULT 'running',  -- running / done / stopped / error
  total            INTEGER NOT NULL DEFAULT 0,       -- 本批番剧总数
  done_count       INTEGER NOT NULL DEFAULT 0,       -- 已处理数
  current_anime_id INTEGER,                          -- 正在处理的番剧 id
  current_title    TEXT,                             -- 正在处理的番剧名
  phase            TEXT,                             -- 当前番剧所处阶段（准备/宽搜/回验）
  skip_feeded      INTEGER NOT NULL DEFAULT 0,       -- 是否跳过已有 RSS 源的番剧（0/1）
  mirror           INTEGER NOT NULL DEFAULT 0,       -- 自动建的源是否开启自动镜像（0/1）
  queue            TEXT,                             -- JSON 数组：启动时的完整列表 [{anime_id,title}]
  items            TEXT,                             -- JSON 数组：逐剧结果（增量写入）[{anime_id,title,result}]
  error            TEXT,
  started_at       TEXT,
  finished_at      TEXT
);
