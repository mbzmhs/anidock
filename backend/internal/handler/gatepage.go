package handler

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ---------- 访客门禁的独立轻页面 ----------
//
// 开启访客认证后，**所有路径**（含 /login、/init）的未认证 HTML 请求
// （爬虫/直接访问）都不下发完整 SPA bundle（1.6MB+ 的前端逻辑与接口地图），
// 而是返回这个自包含的口令页（~2KB，无外部资源）——
// 访客口令即全站安全门禁，防止扫描器在无口令状态下枚举 /login、/init 等路径：
//   - 口令正确 → /api/visitor/login 返回 token 并下发 la_visitor cookie
//   - 前端 JS 把 token 存入 localStorage（SPA 的 XHR 靠它带 Authorization 头），
//     cookie 只用于服务端判断"该给这个请求发 SPA 还是门禁页"（httpOnly，JS 读不到）
//   - 随后跳转 /，此时 cookie 有效 → 服务端发完整 SPA；管理员再走 SPA 内 /login 输后台口令
//
// 未开启访客认证（未初始化、或口令留空）时门禁不生效，全站公开。

const VisitorCookie = "la_visitor"

// gatePageHTML 门禁轻页面（内联样式/脚本，无外部依赖）。
const gatePageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>访客认证 · AniDock 番港</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; margin: 0; }
  body {
    min-height: 100vh; display: flex; align-items: center; justify-content: center;
    padding: 24px; font-family: -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif;
    color: #e8ecf4;
    background:
      radial-gradient(60% 50% at 50% 0%, rgba(91, 140, 255, .08) 0%, transparent 70%),
      #0b0e15;
  }
  .card {
    width: 400px; max-width: 100%; padding: 36px 32px; border-radius: 20px;
    background: linear-gradient(160deg, #161b28 0%, #10141f 100%);
    border: 1px solid #232936; box-shadow: 0 24px 60px rgba(0,0,0,.5);
    display: flex; flex-direction: column; gap: 14px; text-align: center;
  }
  .logo { font-size: 34px; background: linear-gradient(135deg, #5b8cff, #9d6bff); -webkit-background-clip: text; background-clip: text; color: transparent; }
  h1 { font-size: 20px; font-weight: 600; }
  .sub { font-size: 13px; color: #8b93a7; margin-bottom: 8px; }
  input {
    width: 100%; padding: 12px 14px; font-size: 15px; color: #e8ecf4;
    background: #0d1119; border: 1px solid #2a3140; border-radius: 10px; outline: none;
  }
  input:focus { border-color: #5b8cff; }
  button {
    width: 100%; padding: 12px; font-size: 15px; border: none; border-radius: 10px; cursor: pointer;
    background: linear-gradient(135deg, #5b8cff, #9d6bff); color: #fff;
  }
  button:disabled { opacity: .6; cursor: default; }
  .err { color: #ff6b6b; font-size: 13px; min-height: 16px; }
</style>
</head>
<body>
  <div class="card">
    <div class="logo">⚓</div>
    <h1>AniDock · 番港</h1>
    <p class="sub">本站开启访客认证，请输入访客口令</p>
    <input id="pw" type="password" placeholder="访客口令" autocomplete="current-password" />
    <div class="err" id="err"></div>
    <button id="go" onclick="enter()">进入</button>
  </div>
<script>
async function enter() {
  var pw = document.getElementById('pw').value;
  var err = document.getElementById('err');
  var btn = document.getElementById('go');
  if (!pw) { err.textContent = '请输入访客口令'; return; }
  btn.disabled = true; err.textContent = '';
  try {
    var res = await fetch('/api/visitor/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pw }),
    });
    if (res.status === 400) { location.replace('/'); return; } // 认证已被关闭
    var data = await res.json();
    if (!res.ok) { err.textContent = data.error || '认证失败'; btn.disabled = false; return; }
    localStorage.setItem('la_visitor_token', data.token); // SPA 的 XHR 凭证
    location.replace('/');
  } catch (e) { err.textContent = '网络错误，请重试'; btn.disabled = false; }
}
document.getElementById('pw').addEventListener('keydown', function (e) { if (e.key === 'Enter') enter(); });
</script>
</body>
</html>`

// GateCheck 判断一个 HTML 请求是否应返回门禁轻页面（而非完整 SPA）。
// 规则：访客认证已开启 且 请求未携带有效访客/后台 cookie——
// 所有路径一视同仁（/login、/init 同样在门禁之后，管理员也先过访客口令）。
func (a *Admin) GateCheck(r *http.Request) bool {
	if EffectiveVisitorPassword(a.Store, a.Cfg) == "" {
		return false
	}
	c, err := r.Cookie(VisitorCookie)
	if err == nil && c.Value != "" {
		if a.visitors.valid(c.Value) || a.tokens.valid(c.Value) {
			return false
		}
	}
	return true
}

// setVisitorCookie 访客登录成功后下发 cookie（服务端 HTML 分流用；httpOnly，JS 不可读）。
// permanent token 给 10 年有效期，非 permanent 与 token TTL 一致。
func (a *Admin) setVisitorCookie(w http.ResponseWriter, token string, permanent bool) {
	maxAge := 0
	switch {
	case permanent:
		maxAge = 10 * 365 * 24 * 3600
	default:
		if d := a.tokenTTLFunc(); d > 0 {
			maxAge = int(d.Seconds())
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     VisitorCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true, // 仅供服务端 HTML 分流读取；SPA 的 XHR 凭证走 localStorage
		SameSite: http.SameSiteLaxMode,
	})
}

// StaticFrontend 若 distDir 存在则托管编译产物，SPA 回退 index.html。
// gate 为"是否返回访客门禁轻页面"的判断（nil = 无门禁，始终发 SPA）。
func StaticFrontend(distDir string, gate func(*http.Request) bool) (http.Handler, bool) {
	if distDir == "" {
		return nil, false
	}
	index := filepath.Join(distDir, "index.html")
	if st, err := os.Stat(index); err != nil || st.IsDir() {
		return nil, false
	}
	absDist, err := filepath.Abs(distDir)
	if err != nil {
		return nil, false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(absDist, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			// 带 hash 的静态资源（js/css/图片）内容寻址，可永久缓存
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			http.ServeFile(w, r, p)
			return
		}
		// SPA 回退：开启访客认证且未认证时，只发门禁轻页面（不下发完整 bundle）
		if gate != nil && gate(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, strings.TrimSpace(gatePageHTML))
			return
		}
		// index.html 必须每次重新验证（no-cache），否则发新版后浏览器拿旧入口页
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(absDist, "index.html"))
	}), true
}
