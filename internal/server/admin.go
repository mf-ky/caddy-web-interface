package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mf-ky/caddy-web-interface/internal/remote"
	"github.com/mf-ky/caddy-web-interface/internal/store"
)

// ---- settings ----

func (s *Server) settingsView() map[string]any {
	return map[string]any{
		"settings":  s.state.Settings(),
		"publicKey": s.publicKey,
		"dataDir":   s.dataDir,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, s.settingsView())
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in store.Settings
	if !readJSON(w, r, &in) {
		return
	}
	c := in.Connection
	c.Host = strings.TrimSpace(c.Host)
	if c.Mode != "local" {
		c.Mode = "ssh"
	}
	if c.Port < 0 || c.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "Port must be between 1 and 65535.")
		return
	}
	if strings.ContainsAny(c.Host, " /@") {
		writeErr(w, http.StatusBadRequest, "Host should be just a name or IP address, like 192.168.0.10.")
		return
	}
	if in.BackupRetention < 1 || in.BackupRetention > 1000 {
		writeErr(w, http.StatusBadRequest, "Keep between 1 and 1000 backups.")
		return
	}
	if in.SessionHours < 1 {
		in.SessionHours = 24 * 7
	}
	prev := s.state.Settings()
	set, err := s.state.UpdateSettings(func(st *store.Settings) {
		// the host key is only changed through "trust"; reset it if the target moved
		hostKey := st.Connection.HostKey
		if c.Host != st.Connection.Host || c.Port != st.Connection.Port || c.Mode != st.Connection.Mode {
			hostKey = ""
		}
		c.HostKey = hostKey
		st.Connection = c
		st.BackupRetention = in.BackupRetention
		st.SessionHours = in.SessionHours
		st.Offsite.Enabled = in.Offsite.Enabled
		st.Offsite.Target = strings.TrimSpace(in.Offsite.Target)
		st.Offsite.Port = in.Offsite.Port
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if set.Connection != prev.Connection {
		s.client.SetConfig(set.Connection)
		s.cacheMu.Lock()
		s.info, s.dns, s.lastFetch, s.liveErr = nil, nil, time.Time{}, nil
		s.cacheMu.Unlock()
	}
	if set.BackupRetention < prev.BackupRetention && set.Connection.Configured() {
		_ = s.client.Prune(r.Context(), set.BackupRetention)
		s.pruneMirror(set.BackupRetention)
	}
	writeJSON(w, s.settingsView())
}

// handleConnTest checks the connection and reports the host key when it
// still needs to be trusted.
func (s *Server) handleConnTest(w http.ResponseWriter, r *http.Request, u *store.User) {
	cfg := s.client.Config()
	if !cfg.Configured() {
		writeErr(w, http.StatusBadRequest, "Enter the Caddy server's address first.")
		return
	}
	info, err := s.client.Info(r.Context())
	if err != nil {
		var hk *remote.HostKeyError
		if errors.As(err, &hk) {
			writeJSON(w, map[string]any{"ok": false, "needsTrust": true, "fingerprint": hk.Fingerprint, "changed": hk.Expected != "", "message": hk.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "message": remote.Explain(err).Message})
		return
	}
	s.cacheMu.Lock()
	s.info, s.infoAt, s.lastFetch = info, time.Now(), time.Time{}
	s.cacheMu.Unlock()
	msg := "Connected to " + info.Hostname + " (Caddy " + info.CaddyVersion + ")."
	if !info.Writable {
		msg += " Warning: the agent can't write " + info.Caddyfile + " — re-run the installer."
	}
	writeJSON(w, map[string]any{"ok": true, "info": info, "message": msg})
}

func (s *Server) handleConnTrust(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Fingerprint string `json:"fingerprint"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !strings.HasPrefix(in.Fingerprint, "SHA256:") {
		writeErr(w, http.StatusBadRequest, "invalid fingerprint")
		return
	}
	set, err := s.state.UpdateSettings(func(st *store.Settings) { st.Connection.HostKey = in.Fingerprint })
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.client.SetConfig(set.Connection)
	s.handleConnTest(w, r, u)
}

// ---- users ----

type userView struct {
	Username    string     `json:"username"`
	Role        store.Role `json:"role"`
	RoleLabel   string     `json:"roleLabel"`
	Created     time.Time  `json:"created"`
	PasswordSet time.Time  `json:"passwordSet"`
	LastLogin   time.Time  `json:"lastLogin"`
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request, u *store.User) {
	out := []userView{}
	for _, x := range s.users.List() {
		out = append(out, userView{x.Username, x.Role, store.RoleLabel(x.Role), x.Created, x.PasswordSet, x.LastLogin})
	}
	writeJSON(w, map[string]any{"users": out})
}

type userInput struct {
	Username string     `json:"username"`
	Role     store.Role `json:"role"`
	Password string     `json:"password"`
}

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in userInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.users.Add(strings.TrimSpace(in.Username), in.Role, in.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.handleUsers(w, r, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	name := r.PathValue("name")
	var in userInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.Role != "" {
		if err := s.users.SetRole(name, in.Role); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if in.Password != "" {
		if err := s.users.SetPassword(name, in.Password); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.EqualFold(name, u.Username) {
			s.setSession(w, r, s.users.Get(name))
		}
	}
	s.handleUsers(w, r, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *store.User) {
	name := r.PathValue("name")
	if strings.EqualFold(name, u.Username) {
		writeErr(w, http.StatusBadRequest, "You can't delete your own account.")
		return
	}
	if err := s.users.Delete(name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.handleUsers(w, r, u)
}
