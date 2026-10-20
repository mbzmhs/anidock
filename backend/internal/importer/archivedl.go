package importer

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"anidock/backend/internal/store"
)

// ---------- Bangumi Archive 最新归档自动下载 ----------
//
// 容器部署时访问不了宿主机的归档目录，这里提供：
//   - 从 bangumi/Archive 的 aux/latest.json 解析最新归档的下载地址
//     （raw.githubusercontent 为主，GitHub API releases/latest 兜底）；
//   - 后台下载 + 解压到固定目录（DB 所在目录下的 archive/，Docker 里即 /app/data/archive 卷）；
//   - 磁盘上只保留最近一份归档：重新下载成功（新目录就位）后原子删除旧归档；
//   - 下次可复用上次下载（按设置表里的元数据 + 目录实际存在判定），无需重下。

// 最新归档地址源（latest.json 是 GitHub release asset 元数据的 JSON）
const (
	archiveLatestRawURL = "https://raw.githubusercontent.com/bangumi/Archive/master/aux/latest.json"
	archiveLatestAPIURL = "https://api.github.com/repos/bangumi/Archive/releases/latest"
)

// ARCHIVE_LATEST_JSON 环境变量可指定自托管的 latest.json（代理/镜像场景），
// 设置后优先于 GitHub 源（仍保留 GitHub 源兜底）。
func archiveLatestSources() []struct {
	url string
	get func([]byte) (*ArchiveLatest, error)
} {
	srcs := []struct {
		url string
		get func([]byte) (*ArchiveLatest, error)
	}{
		{archiveLatestRawURL, parseArchiveLatestRaw},
		{archiveLatestAPIURL, parseArchiveLatestAPI},
	}
	if v := strings.TrimSpace(os.Getenv("ARCHIVE_LATEST_JSON")); v != "" {
		srcs = append([]struct {
			url string
			get func([]byte) (*ArchiveLatest, error)
		}{{v, parseArchiveLatestRaw}}, srcs...)
	}
	return srcs
}

// ArchiveLatest 最新归档信息（来自 latest.json / GitHub API）。
type ArchiveLatest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at,omitempty"`
	Digest    string `json:"digest,omitempty"` // 如 "sha256:…"
	Source    string `json:"source"`           // raw | api
}

// ArchiveCacheInfo 本地已缓存归档（最近一份）。
type ArchiveCacheInfo struct {
	Available    bool   `json:"available"`
	Name         string `json:"name,omitempty"`
	Dir          string `json:"dir,omitempty"`
	DownloadedAt string `json:"downloaded_at,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type archiveCacheMeta struct {
	Name         string `json:"name"`
	Dir          string `json:"dir"`
	DownloadedAt string `json:"downloaded_at"`
	Size         int64  `json:"size"`
}

// ArchiveDLStatus 下载任务状态快照（前端轮询）。
type ArchiveDLStatus struct {
	Running    bool   `json:"running"`
	Phase      string `json:"phase"` // idle | fetching | extracting | done | error
	Name       string `json:"name,omitempty"`
	TotalBytes int64  `json:"total_bytes"`
	GotBytes   int64  `json:"got_bytes"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
}

type archiveDLJob struct {
	mu      sync.Mutex
	running bool
	phase   string
	name    string
	total   int64
	got     int64
	errMsg  string
	started time.Time
}

var archiveDL = &archiveDLJob{}

// ArchiveBaseDir 归档缓存固定目录：DB 所在目录下的 archive/（Docker 即 /app/data/archive 卷，重启不丢）。
func ArchiveBaseDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "archive")
}

// FetchArchiveLatest 拉取最新归档信息（30 分钟内存缓存，减少对 GitHub 的请求）。
func FetchArchiveLatest() (*ArchiveLatest, error) {
	latestMu.Lock()
	cached, exp := latestInfo, latestExp
	latestMu.Unlock()
	if cached != nil && time.Now().Before(exp) {
		return cached, nil
	}
	var lastErr error
	for _, src := range archiveLatestSources() {
		l, err := fetchArchiveLatestFrom(src.url, src.get)
		if err != nil {
			lastErr = err
			continue
		}
		latestMu.Lock()
		latestInfo, latestExp = l, time.Now().Add(30*time.Minute)
		latestMu.Unlock()
		return l, nil
	}
	return nil, fmt.Errorf("获取最新归档信息失败: %w", lastErr)
}

var latestMu sync.Mutex
var latestInfo *ArchiveLatest
var latestExp time.Time

func fetchArchiveLatestFrom(url string, parse func([]byte) (*ArchiveLatest, error)) (*ArchiveLatest, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	client.Transport = httpTransportWithUA("anidock-archive-downloader")
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parse(body)
}

// httpTransportWithUA 给请求带上 UA（GitHub 对无 UA/默认 UA 的客户端较敏感）。
func httpTransportWithUA(ua string) http.RoundTripper {
	return &uaRT{base: http.DefaultTransport, ua: ua}
}

type uaRT struct {
	base http.RoundTripper
	ua   string
}

func (t *uaRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(context.Background())
	req.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(req)
}

// parseArchiveLatestRaw latest.json 即 release asset 元数据：
// {name, browser_download_url, size, created_at, digest}
func parseArchiveLatestRaw(body []byte) (*ArchiveLatest, error) {
	var raw struct {
		Name        string `json:"name"`
		URL         string `json:"browser_download_url"`
		Size        int64  `json:"size"`
		CreatedAt   string `json:"created_at"`
		Digest      string `json:"digest"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if raw.Name == "" || raw.URL == "" {
		return nil, errors.New("latest.json 缺少名称或下载地址")
	}
	return &ArchiveLatest{Name: raw.Name, URL: raw.URL, Size: raw.Size, CreatedAt: raw.CreatedAt, Digest: raw.Digest, Source: "raw"}, nil
}

// parseArchiveLatestAPI GitHub API releases/latest → 取第一个 asset。
func parseArchiveLatestAPI(body []byte) (*ArchiveLatest, error) {
	var rel struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	if len(rel.Assets) == 0 || rel.Assets[0].Name == "" || rel.Assets[0].BrowserDownloadURL == "" {
		return nil, errors.New("GitHub API 未返回归档资源")
	}
	a := rel.Assets[0]
	return &ArchiveLatest{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size, CreatedAt: rel.CreatedAt, Source: "api"}, nil
}

// ArchiveCacheInfo 读取缓存元数据；目录实际不存在则视为不可用。
func CacheArchiveInfo(st *store.Store, baseDir string) ArchiveCacheInfo {
	v, ok := st.GetSetting("archive_cache")
	if !ok {
		return ArchiveCacheInfo{}
	}
	var meta archiveCacheMeta
	if err := json.Unmarshal([]byte(v), &meta); err != nil || meta.Dir == "" {
		return ArchiveCacheInfo{}
	}
	if fi, err := os.Stat(meta.Dir); err != nil || !fi.IsDir() {
		return ArchiveCacheInfo{}
	}
	if _, err := os.Stat(filepath.Join(meta.Dir, "subject.jsonlines")); err != nil {
		return ArchiveCacheInfo{}
	}
	return ArchiveCacheInfo{Available: true, Name: meta.Name, Dir: meta.Dir, DownloadedAt: meta.DownloadedAt, Size: meta.Size}
}

// ArchiveDownloadStatus 下载任务状态快照。
func ArchiveDownloadStatus() ArchiveDLStatus {
	j := archiveDL
	j.mu.Lock()
	defer j.mu.Unlock()
	st := ArchiveDLStatus{Running: j.running, Phase: j.phase, Name: j.name, TotalBytes: j.total, GotBytes: j.got, Error: j.errMsg}
	if !j.started.IsZero() {
		st.StartedAt = j.started.Format("15:04:05")
	}
	return st
}

// StartArchiveDownload 启动最新归档下载任务（后台执行）。
// force=false 且已有可用缓存时不启动（返回 nil，由前端提示"已缓存"）。
func StartArchiveDownload(st *store.Store, baseDir string, latest *ArchiveLatest, force bool) error {
	if !force {
		if ci := CacheArchiveInfo(st, baseDir); ci.Available {
			return nil
		}
	}
	j := archiveDL
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return errors.New("已有下载任务在进行")
	}
	j.running = true
	j.phase = "fetching"
	j.name = latest.Name
	j.total, j.got = latest.Size, 0
	j.errMsg = ""
	j.started = time.Now()
	j.mu.Unlock()

	go runArchiveDownload(st, baseDir, latest)
	return nil
}

func (j *archiveDLJob) setPhase(p string) {
	j.mu.Lock()
	j.phase = p
	j.mu.Unlock()
}
func (j *archiveDLJob) setProgress(got, total int64) {
	j.mu.Lock()
	j.got, j.total = got, total
	if total <= 0 {
		// 无 Content-Length 时以已下载量展示
		j.total = got
	}
	j.mu.Unlock()
}
func (j *archiveDLJob) fail(err error) {
	j.mu.Lock()
	j.running = false
	j.phase = "error"
	j.errMsg = err.Error()
	j.mu.Unlock()
}
func (j *archiveDLJob) finish() {
	j.mu.Lock()
	j.running = false
	j.phase = "done"
	j.mu.Unlock()
}

// runArchiveDownload 下载（带重试与 sha256 校验）→ 解压 → 原子替换旧归档。
func runArchiveDownload(st *store.Store, baseDir string, latest *ArchiveLatest) {
	j := archiveDL
	defer func() {
		if r := recover(); r != nil {
			j.fail(fmt.Errorf("下载panic: %v", r))
		}
	}()

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		j.fail(fmt.Errorf("创建缓存目录: %w", err))
		return
	}
	zipPart := filepath.Join(baseDir, latest.Name+".part")
	stem := strings.TrimSuffix(latest.Name, ".zip")
	tmpDir := filepath.Join(baseDir, stem+".tmp")

	// ---- 1. 下载（失败重试 3 次，整包重下；GitHub 跳转由 http.Client 自动跟随）----
	client := &http.Client{Transport: httpTransportWithUA("anidock-archive-downloader")} // 大文件不设整体超时
	var resp *http.Response
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, lastErr = client.Get(latest.URL)
		if lastErr != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("下载地址返回 %d", resp.StatusCode)
			continue
		}
		if err := streamArchive(resp.Body, zipPart, latest, j); err == nil {
			resp.Body.Close()
			lastErr = nil
			break
		}
		resp.Body.Close()
		os.Remove(zipPart) // 断点不续传，重来
	}
	if lastErr != nil {
		j.fail(fmt.Errorf("下载失败: %w", lastErr))
		return
	}

	// ---- 2. sha256 校验（latest.json 提供 digest 时）----
	if strings.HasPrefix(latest.Digest, "sha256:") {
		want := strings.TrimPrefix(latest.Digest, "sha256:")
		got, err := hashFile(zipPart)
		if err != nil {
			j.fail(fmt.Errorf("校验归档: %w", err))
			return
		}
		if !strings.EqualFold(got, want) {
			os.Remove(zipPart)
			j.fail(errors.New("归档 sha256 校验不一致，已丢弃"))
			return
		}
	}

	// ---- 3. 解压到临时目录 ----
	j.setPhase("extracting")
	if err := os.RemoveAll(tmpDir); err != nil {
		j.fail(err)
		return
	}
	if err := extractZip(zipPart, tmpDir); err != nil {
		os.Remove(zipPart)
		j.fail(fmt.Errorf("解压归档: %w", err))
		return
	}
	if fi, err := os.Stat(filepath.Join(tmpDir, "subject.jsonlines")); err != nil || fi.Size() == 0 {
		os.RemoveAll(tmpDir)
		os.Remove(zipPart)
		j.fail(errors.New("归档内容不完整（缺少 subject.jsonlines）"))
		return
	}

	// ---- 4. 原子替换：新目录就位 → 删除旧归档 → 落元数据 ----
	finalDir := filepath.Join(baseDir, stem)
	finalZip := filepath.Join(baseDir, latest.Name)
	if err := os.RemoveAll(finalDir); err != nil { // 同名重下：先清掉旧的同名目录
		j.fail(err)
		return
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		os.RemoveAll(tmpDir)
		j.fail(fmt.Errorf("归档就位: %w", err))
		return
	}
	if err := os.Rename(zipPart, finalZip); err != nil {
		j.fail(fmt.Errorf("归档压缩包就位: %w", err))
		return
	}
	// 新归档已就位：清掉目录里其余旧归档（zip + 解压目录）
	entries, _ := os.ReadDir(baseDir)
	for _, e := range entries {
		name := e.Name()
		if name == stem || name == latest.Name {
			continue
		}
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".part") {
			os.Remove(filepath.Join(baseDir, name))
		} else if e.IsDir() && !strings.HasSuffix(name, ".tmp") {
			os.RemoveAll(filepath.Join(baseDir, name))
		}
	}
	// 元数据落 settings 表（容器重启后仍可识别缓存）
	meta, _ := json.Marshal(archiveCacheMeta{
		Name: latest.Name, Dir: finalDir,
		DownloadedAt: time.Now().Format(time.RFC3339),
		Size:         latest.Size,
	})
	if err := st.SetSetting("archive_cache", string(meta)); err != nil {
		j.fail(fmt.Errorf("写入缓存元数据: %w", err))
		return
	}
	j.setProgress(latest.Size, latest.Size)
	j.finish()
}

// streamArchive 流式下载并更新进度；返回错误表示中断。
func streamArchive(rc io.Reader, dest string, latest *ArchiveLatest, j *archiveDLJob) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	hw := &progressWriter{Writer: out, total: latest.Size, j: j}
	if _, err := io.Copy(hw, rc); err != nil {
		return err
	}
	return out.Sync()
}

// progressWriter 边写边报进度（每秒最多上报一次，避免轮询抖动）。
type progressWriter struct {
	io.Writer
	total int64
	got   int64
	j     *archiveDLJob
	last  time.Time
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.got += int64(n)
	now := time.Now()
	if now.Sub(w.last) > time.Second {
		w.last = now
		w.j.setProgress(w.got, w.total)
	}
	return n, err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractZip 解压 zip 到 dir（含 zip-slip 防护；归档内均为顶层 jsonlines 文件）。
func extractZip(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target := filepath.Join(dest, filepath.Clean("/"+f.Name))
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("非法归档路径: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
