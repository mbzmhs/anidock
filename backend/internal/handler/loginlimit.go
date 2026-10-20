package handler

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- 登录类接口防爆破 ----------
//
// 双保险（按键 "IP|接口"，访客登录 / 后台登录 / 首次初始化各自独立计数）：
//  1. 窗口限流：同一 IP 在 window（10min）内最多 maxBurst（10）次请求，超出返回 429；
//  2. 连续失败锁定：窗口内失败（401）累计达 failLimit（5）次，该 IP 被锁定 lockFor（15min），
//     锁定期间即使口令正确也返回 429（带 Retry-After 秒数）。
//
// 仅内存计数、不落库：进程重启后计数清零（可接受——token 签发本身无放大效应）。
// 源 IP 识别：Cloudflare 注入的 CF-Connecting-IP（客户端无法伪造）优先，
// 其次 X-Forwarded-For 首项、X-Real-IP，最后 RemoteAddr（直连/本地场景准确）。

type loginLimiter struct {
	mu        sync.Mutex
	entries   map[string]*limEntry
	window    time.Duration
	maxBurst  int
	failLimit int
	lockFor   time.Duration
}

type limEntry struct {
	windowStart time.Time
	count       int // 窗口内请求次数
	fails       int // 窗口内失败次数
	lockUntil   time.Time
}

func newLoginLimiter() *loginLimiter {
	l := &loginLimiter{
		entries:   map[string]*limEntry{},
		window:    10 * time.Minute,
		maxBurst:  10,
		failLimit: 5,
		lockFor:   15 * time.Minute,
	}
	go l.gcLoop()
	return l
}

// gcLoop 定期清理长期无活动的条目，防止 map 无限增长。
func (l *loginLimiter) gcLoop() {
	for range time.NewTicker(5 * time.Minute).C {
		l.mu.Lock()
		now := time.Now()
		for k, e := range l.entries {
			if e.lockUntil.Before(now) && now.Sub(e.windowStart) > l.window {
				delete(l.entries, k)
			}
		}
		l.mu.Unlock()
	}
}

// hit 记录一次请求，返回（是否放行, 需等待秒数）。
func (l *loginLimiter) hit(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e, ok := l.entries[key]
	if !ok {
		e = &limEntry{windowStart: now}
		l.entries[key] = e
	}
	if !e.lockUntil.IsZero() && e.lockUntil.After(now) {
		sec := int(e.lockUntil.Sub(now).Seconds())
		if sec < 1 {
			sec = 1
		}
		return false, sec
	}
	if now.Sub(e.windowStart) > l.window {
		e.windowStart, e.count, e.fails = now, 0, 0
	}
	if e.count >= l.maxBurst {
		sec := int(e.windowStart.Add(l.window).Sub(now).Seconds())
		if sec < 1 {
			sec = 1
		}
		return false, sec
	}
	e.count++
	return true, 0
}

// recordFailure 记录一次认证失败，达到阈值则锁定该 IP。
func (l *loginLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return
	}
	e.fails++
	if e.fails >= l.failLimit {
		e.lockUntil = time.Now().Add(l.lockFor)
		e.fails = 0
	}
}

// LoginGuard 给登录类 handler 套上防爆破保护（窗口限流 + 连续失败锁定）。
func (a *Admin) LoginGuard(endpoint string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := clientIP(r) + "|" + endpoint
		if ok, ra := a.limiter.hit(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(ra))
			writeErr(w, 429, fmt.Errorf("请求过于频繁，请 %d 秒后重试", ra))
			return
		}
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		h(rw, r)
		if rw.status == 401 {
			a.limiter.recordFailure(key)
		}
	}
}

// statusRecorder 捕获 handler 写入的响应状态码（用于判定认证是否失败）。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	return s.ResponseWriter.Write(b)
}

// clientIP 识别请求源 IP（限流键用）。
func clientIP(r *http.Request) string {
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); ip != nil {
		return ip.String()
	}
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		first := f
		if i := strings.IndexByte(f, ','); i >= 0 {
			first = f[:i]
		}
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
