package store

import (
	"sync"
	"time"

	"github.com/mf-ky/caddy-web-interface/internal/remote"
)

// Offsite is an optional extra copy of backups made with rsync.
type Offsite struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target"` // e.g. user@nas:/backups/caddy/ or /mnt/backup/caddy/
	Port    int    `json:"port"`   // SSH port for remote targets (0 = 22)
	LastRun string `json:"lastRun,omitempty"`
	LastErr string `json:"lastError,omitempty"`
}

// Settings are the admin-editable options.
type Settings struct {
	Connection      remote.Config `json:"connection"`
	BackupRetention int           `json:"backupRetention"`
	Offsite         Offsite       `json:"offsite"`
	SessionHours    int           `json:"sessionHours"`
}

// Change is one entry in the draft's change log.
type Change struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user"`
	Action string    `json:"action"` // added | edited | deleted | reverted | raw-edit
	Target string    `json:"target"`
}

// Draft is the working copy the UI edits before "Apply".
type Draft struct {
	Text     string            `json:"text"`
	BaseSHA  string            `json:"baseSha"` // SHA of the live file the draft started from
	Rev      int64             `json:"rev"`
	Creators map[string]string `json:"creators"` // segment key -> username, for cards added in this draft
	Log      []Change          `json:"log"`
}

// Live caches the most recently read server Caddyfile.
type Live struct {
	Text    string    `json:"text"`
	SHA     string    `json:"sha"`
	Fetched time.Time `json:"fetched"`
}

// ApplyRecord is one entry of the history page.
type ApplyRecord struct {
	Time    time.Time `json:"time"`
	User    string    `json:"user"`
	Action  string    `json:"action"` // apply | restore
	Backup  string    `json:"backup,omitempty"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Changes []Change  `json:"changes,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// State bundles the server-owned JSON files.
type State struct {
	dir string
	mu  sync.Mutex

	settings Settings
	draft    Draft
	live     Live
	history  []ApplyRecord
}

// OpenState loads settings, draft, live cache and history from dir.
func OpenState(dir string) (*State, error) {
	s := &State{dir: dir}
	s.settings = Settings{BackupRetention: 20, SessionHours: 24 * 7, Connection: remote.Config{Mode: "ssh", Port: 22, User: "caddyweb"}}
	for _, f := range []struct {
		name string
		v    any
	}{{"settings.json", &s.settings}, {"draft.json", &s.draft}, {"live.json", &s.live}, {"history.json", &s.history}} {
		if err := readJSON(dir+"/"+f.name, f.v); err != nil {
			return nil, err
		}
	}
	if s.settings.BackupRetention < 1 {
		s.settings.BackupRetention = 20
	}
	if s.settings.SessionHours < 1 {
		s.settings.SessionHours = 24 * 7
	}
	if s.draft.Creators == nil {
		s.draft.Creators = map[string]string{}
	}
	return s, nil
}

// Settings returns a copy of the settings.
func (s *State) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// UpdateSettings applies fn and saves.
func (s *State) UpdateSettings(fn func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.settings)
	return s.settings, writeJSON(s.dir+"/settings.json", s.settings, 0o600)
}

// Draft returns a copy of the draft.
func (s *State) Draft() Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyDraft(s.draft)
}

func copyDraft(d Draft) Draft {
	c := d
	c.Creators = map[string]string{}
	for k, v := range d.Creators {
		c.Creators[k] = v
	}
	c.Log = append([]Change(nil), d.Log...)
	return c
}

// SetDraft saves a new draft.
func (s *State) SetDraft(d Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draft = copyDraft(d)
	return writeJSON(s.dir+"/draft.json", s.draft, 0o600)
}

// Live returns the cached live file.
func (s *State) Live() Live {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live
}

// SetLive caches the live file.
func (s *State) SetLive(l Live) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := l.SHA != s.live.SHA
	s.live = l
	if !changed {
		return nil
	}
	return writeJSON(s.dir+"/live.json", s.live, 0o600)
}

// History returns apply history, newest first.
func (s *State) History() []ApplyRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ApplyRecord(nil), s.history...)
}

// AddHistory records an apply/restore (keeps the latest 500).
func (s *State) AddHistory(r ApplyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append([]ApplyRecord{r}, s.history...)
	if len(s.history) > 500 {
		s.history = s.history[:500]
	}
	return writeJSON(s.dir+"/history.json", s.history, 0o600)
}
