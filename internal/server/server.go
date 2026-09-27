// Package server is CaddyWeb's HTTP API and static file server.
package server

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/mf-ky/caddy-web-interface/agent"
	"github.com/mf-ky/caddy-web-interface/internal/remote"
	"github.com/mf-ky/caddy-web-interface/internal/store"
)

// Server holds everything the handlers need.
type Server struct {
	dataDir   string
	version   string
	users     *store.Users
	state     *store.State
	signer    ssh.Signer
	publicKey string
	secret    []byte
	static    fs.FS

	limiter limiter
	setupMu sync.Mutex

	ctxMu sync.Mutex
	ctxs  map[string]*serverCtx
}

// Options configure New.
type Options struct {
	DataDir string
	Version string
	Static  fs.FS
}

// New opens the data directory and prepares the server.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	users, err := store.OpenUsers(opts.DataDir)
	if err != nil {
		return nil, err
	}
	st, err := store.OpenState(opts.DataDir)
	if err != nil {
		return nil, err
	}
	secret, err := loadSecret(filepath.Join(opts.DataDir, "session.key"))
	if err != nil {
		return nil, err
	}
	signer, pub, err := remote.LoadOrCreateKey(filepath.Join(opts.DataDir, "ssh"))
	if err != nil {
		return nil, err
	}
	s := &Server{
		dataDir: opts.DataDir, version: opts.Version, users: users, state: st,
		publicKey: pub, secret: secret, static: opts.Static, signer: signer,
		ctxs: map[string]*serverCtx{},
	}
	return s, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// public
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("GET /agent/install.sh", s.handleInstaller)

	// any logged-in user
	any := func(h func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
		return s.require(h, store.RoleViewer, store.RolePower, store.RoleAdmin)
	}
	power := func(h func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
		return s.require(h, store.RolePower, store.RoleAdmin)
	}
	admin := func(h func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
		return s.require(h, store.RoleAdmin)
	}

	mux.HandleFunc("POST /api/me/password", any(s.handleChangePassword))
	mux.HandleFunc("GET /api/servers", any(s.handleServers))
	mux.HandleFunc("POST /api/tools/hash-password", power(s.handleHashPassword))

	// admin only: servers, users, global settings
	mux.HandleFunc("POST /api/servers", admin(s.handleAddServer))
	mux.HandleFunc("GET /api/settings", admin(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", admin(s.handlePutSettings))
	mux.HandleFunc("POST /api/offsite/run", admin(s.handleOffsiteRun))
	mux.HandleFunc("GET /api/users", admin(s.handleUsers))
	mux.HandleFunc("POST /api/users", admin(s.handleAddUser))
	mux.HandleFunc("PUT /api/users/{name}", admin(s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/users/{name}", admin(s.handleDeleteUser))

	// per server: /api/servers/{sid}/...
	viewer := []store.Role{store.RoleViewer, store.RolePower, store.RoleAdmin}
	powerRoles := []store.Role{store.RolePower, store.RoleAdmin}
	adminRole := []store.Role{store.RoleAdmin}
	srv := func(pattern string, h serverHandler, roles []store.Role) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" /api/servers/{sid}"+path, s.onServer(h, roles...))
	}
	srv("GET ", s.handleServer, viewer)
	srv("GET /state", s.handleState, viewer)
	srv("GET /draft/diff", s.handleDraftDiff, viewer)
	srv("GET /caddyfile", s.handleCaddyfileText, viewer)
	srv("GET /history", s.handleHistory, viewer)
	srv("GET /backups", s.handleBackups, viewer)
	srv("GET /backups/{name}", s.handleBackupRead, viewer)
	srv("GET /dns-providers", s.handleDNSProviders, viewer)

	// power users may add new cards (and edit the ones they added before Apply)
	srv("POST /draft/segments", s.handleAddSegment, powerRoles)
	srv("PUT /draft/segments/{id}", s.handleUpdateSegment, powerRoles)
	srv("PUT /draft/segments/{id}/raw", s.handleUpdateSegmentRaw, powerRoles)
	srv("POST /draft/segments/{id}/copy", s.handleCopySegment, powerRoles)
	srv("POST /draft/preview", s.handlePreview, powerRoles)
	srv("POST /validate", s.handleValidate, powerRoles)

	srv("PUT ", s.handleUpdateServer, adminRole)
	srv("DELETE ", s.handleDeleteServer, adminRole)
	srv("POST /test", s.handleConnTest, adminRole)
	srv("POST /trust", s.handleConnTrust, adminRole)
	srv("DELETE /draft/segments/{id}", s.handleDeleteSegment, adminRole)
	srv("POST /draft/segments/{id}/revert", s.handleRevertSegment, adminRole)
	srv("POST /draft/restore-deleted", s.handleRestoreDeleted, adminRole)
	srv("PUT /draft/raw", s.handleDraftRaw, adminRole)
	srv("POST /draft/discard", s.handleDiscard, adminRole)
	srv("POST /draft/rebase", s.handleRebase, adminRole)
	srv("POST /apply", s.handleApply, adminRole)
	srv("POST /backups/{name}/restore", s.handleRestore, adminRole)
	srv("GET /backups/{name}/download", s.handleBackupDownload, adminRole)

	mux.Handle("/", s.staticHandler())
	return s.secure(mux)
}

// secure adds security headers and blocks cross-site API writes.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				// Browsers can't add custom headers to cross-site form posts.
				if r.Header.Get("X-CaddyWeb") != "1" {
					writeErr(w, http.StatusForbidden, "missing X-CaddyWeb header")
					return
				}
				if o := r.Header.Get("Origin"); o != "" {
					if u, err := url.Parse(o); err != nil || u.Host != r.Host {
						writeErr(w, http.StatusForbidden, "cross-origin request refused")
						return
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) require(h func(http.ResponseWriter, *http.Request, *store.User), roles ...store.Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "Please log in.")
			return
		}
		for _, role := range roles {
			if u.Role == role {
				h(w, r, u)
				return
			}
		}
		writeErr(w, http.StatusForbidden, "Your role ("+store.RoleLabel(u.Role)+") is not allowed to do this.")
	}
}

func (s *Server) staticHandler() http.Handler {
	files := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) handleInstaller(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="install-agent.sh"`)
	_, _ = w.Write([]byte(agent.Installer(s.publicKey)))
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writing response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeErrData sends an error with extra structured fields.
func writeErrData(w http.ResponseWriter, code int, msg string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["error"] = msg
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request: "+err.Error())
		return false
	}
	return true
}
