package server

import (
	"net/http"
	"strings"
	"time"

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
	if in.BackupRetention < 1 || in.BackupRetention > 1000 {
		writeErr(w, http.StatusBadRequest, "Keep between 1 and 1000 backups.")
		return
	}
	if in.SessionHours < 1 {
		in.SessionHours = 24 * 7
	}
	if in.Offsite.Port < 0 || in.Offsite.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "The rsync SSH port must be between 1 and 65535.")
		return
	}
	prev := s.state.Settings()
	set, err := s.state.UpdateSettings(func(st *store.Settings) {
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
	if set.BackupRetention < prev.BackupRetention {
		for _, cfg := range s.state.Servers() {
			if sc := s.serverCtx(cfg.ID); sc != nil && cfg.Connection.Configured() {
				_ = sc.client.Prune(r.Context(), set.BackupRetention)
				sc.pruneMirror(set.BackupRetention)
			}
		}
	}
	writeJSON(w, s.settingsView())
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
