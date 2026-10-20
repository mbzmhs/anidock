package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"anidock/backend/internal/store"
)

// ---------- 首次初始化（bootstrap） ----------
// 系统从未设置过后台口令（.env 与 DB 均无）时，必须先经 /api/bootstrap 设置后台口令
//（可选顺带设置访客口令），之后进入正常登录/设置流程。已配置口令的系统不受影响。

// bootstrapLimiter 简单窗口限流：10 分钟内最多 10 次尝试（防对初始化端点爆破）
var bootstrapLimiter = newWindowLimiter(10, 10*time.Minute)

type windowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	count  int
	reset  time.Time
}

func newWindowLimiter(max int, window time.Duration) *windowLimiter {
	return &windowLimiter{max: max, window: window, reset: time.Now().Add(window)}
}

// hit 记一次尝试；超出窗口配额返回 true（应拒绝）
func (l *windowLimiter) hit() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Now().After(l.reset) {
		l.count = 0
		l.reset = time.Now().Add(l.window)
	}
	l.count++
	return l.count > l.max
}

// BootstrapStatus GET /api/bootstrap/status → {"initialized": bool}
func (a *Admin) BootstrapStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]bool{"initialized": EffectiveAdminPassword(a.Store, a.Cfg) != ""})
}

type bootstrapReq struct {
	AdminPassword   string `json:"admin_password"`
	VisitorPassword string `json:"visitor_password"`
}

// Bootstrap POST /api/bootstrap —— 仅在"未配置任何后台口令"时可用
func (a *Admin) Bootstrap(w http.ResponseWriter, r *http.Request) {
	if EffectiveAdminPassword(a.Store, a.Cfg) != "" {
		writeErr(w, 409, errors.New("系统已初始化；修改口令请在后台登录后使用「⚙ 设置」"))
		return
	}
	if bootstrapLimiter.hit() {
		writeErr(w, 429, errors.New("尝试过于频繁，请稍后再试"))
		return
	}
	var req bootstrapReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	admin := strings.TrimSpace(req.AdminPassword)
	if len(admin) < 6 {
		writeErr(w, 400, errors.New("后台口令至少 6 位"))
		return
	}
	visitor := strings.TrimSpace(req.VisitorPassword)
	if visitor != "" && len(visitor) < 6 {
		writeErr(w, 400, errors.New("访客口令至少 6 位（不需要可留空）"))
		return
	}
	if err := a.Store.SetSetting(store.SettingAdminPassword, admin); err != nil {
		writeErr(w, 500, err)
		return
	}
	// 访客口令留空时不写 DB：交由 .env 的 VISITOR_PASSWORD 决定（两者都空 = 站点公开）
	if visitor != "" {
		if err := a.Store.SetSetting(store.SettingVisitorPassword, visitor); err != nil {
			writeErr(w, 500, err)
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"initialized": true})
}
