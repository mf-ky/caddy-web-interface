package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mf-ky/caddy-web-interface/internal/store"
)

const cookieName = "caddyweb_session"

type sessionData struct {
	U string `json:"u"` // username
	S string `json:"s"` // password stamp
	E int64  `json:"e"` // expiry (unix)
}

// loadSecret reads or creates the HMAC key that signs session cookies.
func loadSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, os.WriteFile(path, b, 0o600)
}

func (s *Server) sign(payload []byte) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write(payload)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	hours := s.state.Settings().SessionHours
	exp := time.Now().Add(time.Duration(hours) * time.Hour)
	payload, _ := json.Marshal(sessionData{U: u.Username, S: u.Stamp(), E: exp.Unix()})
	val := base64.RawURLEncoding.EncodeToString(payload) + "." + s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: val, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// currentUser returns the logged-in user, or nil.
func (s *Server) currentUser(r *http.Request) *store.User {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return nil
	}
	var sd sessionData
	if json.Unmarshal(payload, &sd) != nil || time.Now().Unix() > sd.E {
		return nil
	}
	u := s.users.Get(sd.U)
	if u == nil || u.Stamp() != sd.S {
		return nil // deleted, or password/role changed since login
	}
	return u
}

// limiter slows down password guessing per client IP.
type limiter struct {
	mu    sync.Mutex
	fails map[string]*failInfo
}

type failInfo struct {
	count int
	until time.Time
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *limiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f, ok := l.fails[ip]; ok && time.Now().Before(f.until) {
		return time.Until(f.until)
	}
	return 0
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		l.fails = map[string]*failInfo{}
	}
	f := l.fails[ip]
	if f == nil {
		f = &failInfo{}
		l.fails[ip] = f
	}
	f.count++
	if f.count >= 5 {
		d := time.Duration(1<<min(f.count-5, 4)) * time.Minute // 1,2,4,8,16 min
		f.until = time.Now().Add(d)
	}
}

func (l *limiter) ok(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if d := s.limiter.blocked(ip); d > 0 {
		writeErr(w, http.StatusTooManyRequests, "Too many failed attempts. Try again in "+d.Round(time.Second).String()+".")
		return
	}
	var req loginReq
	if !readJSON(w, r, &req) {
		return
	}
	u := s.users.Verify(strings.TrimSpace(req.Username), req.Password)
	if u == nil {
		s.limiter.fail(ip)
		writeErr(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	s.limiter.ok(ip)
	s.setSession(w, r, u)
	writeJSON(w, s.sessionView(u))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) sessionView(u *store.User) map[string]any {
	out := map[string]any{"needsSetup": s.users.Count() == 0, "version": s.version}
	if u != nil {
		out["user"] = map[string]any{"username": u.Username, "role": u.Role, "roleLabel": store.RoleLabel(u.Role)}
	}
	return out
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.sessionView(s.currentUser(r)))
}

// handleSetup creates the first admin account (only while there are no users).
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.users.Count() > 0 {
		writeErr(w, http.StatusForbidden, "Setup is already done.")
		return
	}
	var req loginReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.users.Add(strings.TrimSpace(req.Username), store.RoleAdmin, req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := s.users.Get(strings.TrimSpace(req.Username))
	s.setSession(w, r, u)
	writeJSON(w, s.sessionView(u))
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if s.users.Verify(u.Username, req.Current) == nil {
		writeErr(w, http.StatusBadRequest, "Your current password is not correct.")
		return
	}
	if err := s.users.SetPassword(u.Username, req.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setSession(w, r, s.users.Get(u.Username))
	writeJSON(w, map[string]bool{"ok": true})
}
