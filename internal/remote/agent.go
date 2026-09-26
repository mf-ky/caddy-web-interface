package remote

import (
	"bufio"
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Info is what `caddyweb-agent info` reports.
type Info struct {
	AgentVersion string `json:"agentVersion"`
	Hostname     string `json:"hostname"`
	CaddyVersion string `json:"caddyVersion"`
	Caddyfile    string `json:"caddyfile"`
	BackupDir    string `json:"backupDir"`
	Service      string `json:"service"`
	Writable     bool   `json:"writable"`
}

func parseKV(out []byte) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

// Info fetches server facts.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	out, err := c.Run(ctx, nil, "info")
	if err != nil {
		return nil, err
	}
	m := parseKV(out)
	return &Info{
		AgentVersion: m["agent_version"],
		Hostname:     m["hostname"],
		CaddyVersion: m["caddy_version"],
		Caddyfile:    m["caddyfile"],
		BackupDir:    m["backup_dir"],
		Service:      m["service"],
		Writable:     m["writable"] == "yes",
	}, nil
}

// Read returns the live Caddyfile.
func (c *Client) Read(ctx context.Context) (string, error) {
	out, err := c.Run(ctx, nil, "read")
	return string(out), err
}

// Validate asks Caddy on the server to check content without changing anything.
func (c *Client) Validate(ctx context.Context, content string) error {
	_, err := c.Run(ctx, []byte(content), "validate")
	return err
}

// ApplyResult is returned by Apply and Restore.
type ApplyResult struct {
	Backup string `json:"backup"`
	SHA    string `json:"sha"`
}

// Apply validates, backs up, writes and reloads. expectedSHA guards against
// overwriting changes made on the server in the meantime ("" = don't check).
func (c *Client) Apply(ctx context.Context, content, expectedSHA string) (*ApplyResult, error) {
	if expectedSHA == "" {
		expectedSHA = "-"
	}
	out, err := c.Run(ctx, []byte(content), "apply", expectedSHA)
	if err != nil {
		return nil, err
	}
	m := parseKV(out)
	return &ApplyResult{Backup: m["backup"], SHA: m["sha"]}, nil
}

// Restore makes a backup the live Caddyfile (the current one is backed up first).
func (c *Client) Restore(ctx context.Context, name, expectedSHA string) (*ApplyResult, error) {
	if expectedSHA == "" {
		expectedSHA = "-"
	}
	out, err := c.Run(ctx, nil, "restore", name, expectedSHA)
	if err != nil {
		return nil, err
	}
	m := parseKV(out)
	return &ApplyResult{Backup: m["backup"], SHA: m["sha"]}, nil
}

// Backup describes one backup file on the server.
type Backup struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

var backupNameRe = regexp.MustCompile(`^Caddyfile\.\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}(\.\d+)?$`)

// ValidBackupName checks a name before it is sent to the agent.
func ValidBackupName(n string) bool { return backupNameRe.MatchString(n) }

// Backups lists backups, newest first.
func (c *Client) Backups(ctx context.Context) ([]Backup, error) {
	out, err := c.Run(ctx, nil, "backups")
	if err != nil {
		return nil, err
	}
	var list []Backup
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) != 3 || !ValidBackupName(f[0]) {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		ts, _ := strconv.ParseInt(f[2], 10, 64)
		list = append(list, Backup{Name: f[0], Size: size, Time: time.Unix(ts, 0)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list, nil
}

// BackupRead returns a backup's content.
func (c *Client) BackupRead(ctx context.Context, name string) (string, error) {
	if !ValidBackupName(name) {
		return "", errors.New("invalid backup name")
	}
	out, err := c.Run(ctx, nil, "backup-read", name)
	return string(out), err
}

// Prune keeps the newest `keep` backups.
func (c *Client) Prune(ctx context.Context, keep int) error {
	if keep < 1 {
		return nil
	}
	_, err := c.Run(ctx, nil, "prune", strconv.Itoa(keep))
	return err
}

// DNSProviders lists installed dns.providers.* modules.
func (c *Client) DNSProviders(ctx context.Context) ([]string, error) {
	out, err := c.Run(ctx, nil, "modules")
	if err != nil {
		return nil, err
	}
	var list []string
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "dns.providers.") {
			list = append(list, strings.TrimPrefix(ln, "dns.providers."))
		}
	}
	sort.Strings(list)
	return list, nil
}

// CaddyError turns agent output into something a person can read.
type CaddyError struct {
	Kind    string `json:"kind"` // invalid | reload | conflict | connection | error
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

var lineRe = regexp.MustCompile(`Caddyfile:(\d+)`)
var pathRe = regexp.MustCompile(`/[^\s:'"]*/run\.\d+/Caddyfile`)

// Explain converts an error from Run into a CaddyError.
func Explain(err error) *CaddyError {
	var ae *AgentError
	if !errors.As(err, &ae) {
		var hk *HostKeyError
		if errors.As(err, &hk) {
			return &CaddyError{Kind: "hostkey", Message: hk.Error()}
		}
		return &CaddyError{Kind: "connection", Message: err.Error()}
	}
	detail := cleanCaddyOutput(ae.Stderr)
	ce := &CaddyError{Detail: detail, Message: summarize(detail)}
	switch ae.Code {
	case ExitInvalid:
		ce.Kind = "invalid"
	case ExitReload:
		ce.Kind = "reload"
	case ExitConflict:
		ce.Kind = "conflict"
	default:
		ce.Kind = "error"
	}
	if m := lineRe.FindStringSubmatch(detail); m != nil {
		ce.Line, _ = strconv.Atoi(m[1])
	}
	return ce
}

// cleanCaddyOutput drops Caddy's JSON log lines and tidies temp paths.
func cleanCaddyOutput(s string) string {
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "{\"level\"") || looksLikeTextLog(t) {
			continue
		}
		keep = append(keep, pathRe.ReplaceAllString(t, "Caddyfile"))
	}
	if len(keep) == 0 {
		return strings.TrimSpace(s)
	}
	return strings.Join(keep, "\n")
}

var textLogRe = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d+\s+(INFO|WARN|DEBUG)`)

func looksLikeTextLog(s string) bool { return textLogRe.MatchString(s) }

func summarize(detail string) string {
	for _, ln := range strings.Split(detail, "\n") {
		if strings.HasPrefix(ln, "Error: ") {
			return strings.TrimPrefix(ln, "Error: ")
		}
	}
	lines := strings.Split(strings.TrimSpace(detail), "\n")
	return lines[len(lines)-1]
}
