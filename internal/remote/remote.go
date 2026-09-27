// Package remote talks to caddyweb-agent on the Caddy server, either over
// SSH (CaddyWeb on another machine) or by running it directly (same machine).
package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Exit codes from caddyweb-agent.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitInvalid  = 2
	ExitReload   = 3
	ExitConflict = 4
	ExitNoBackup = 5
)

// Config describes how to reach the agent.
type Config struct {
	Mode      string `json:"mode"` // "ssh" or "local"
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	HostKey   string `json:"hostKey"`   // trusted host key fingerprint (SHA256:...)
	AgentPath string `json:"agentPath"` // local mode only
}

// Configured reports whether enough is set to try connecting.
func (c Config) Configured() bool {
	if c.Mode == "local" {
		return true
	}
	return c.Host != ""
}

// AgentError is a non-zero exit from the agent.
type AgentError struct {
	Code   int
	Stderr string
}

func (e *AgentError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Code < 0 {
		msg = "the agent was stopped before it finished (timeout)"
	} else if msg == "" {
		msg = fmt.Sprintf("agent exited with code %d", e.Code)
	}
	return msg
}

// HostKeyError is returned when the server's key is unknown or changed.
type HostKeyError struct {
	Fingerprint string
	Expected    string
}

func (e *HostKeyError) Error() string {
	if e.Expected == "" {
		return "this server's SSH key has not been trusted yet (" + e.Fingerprint + ")"
	}
	return fmt.Sprintf("the server's SSH key changed! expected %s, got %s. If you reinstalled the server, trust the new key in Settings; otherwise someone may be impersonating it", e.Expected, e.Fingerprint)
}

// Client runs agent commands. It keeps one SSH connection open and
// reconnects when needed.
type Client struct {
	mu      sync.Mutex
	cfg     Config
	signer  ssh.Signer
	conn    *ssh.Client
	timeout time.Duration
}

// NewClient creates a client; signer may be nil in local mode.
func NewClient(cfg Config, signer ssh.Signer) *Client {
	return &Client{cfg: cfg, signer: signer, timeout: 60 * time.Second}
}

// SetConfig swaps the connection settings (closing any open connection).
func (c *Client) SetConfig(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Config returns the current settings.
func (c *Client) Config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

var safeArg = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// Run executes an agent command with optional stdin.
func (c *Client) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	for _, a := range args {
		if !safeArg.MatchString(a) {
			return nil, fmt.Errorf("refusing unsafe agent argument %q", a)
		}
	}
	cfg := c.Config()
	if !cfg.Configured() {
		return nil, errors.New("CaddyWeb is not connected to a Caddy server yet. An admin can set this up in Settings → Connection")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if cfg.Mode == "local" {
		return runLocal(ctx, cfg, stdin, args)
	}
	c.mu.Lock()
	hadConn := c.conn != nil
	c.mu.Unlock()
	out, err := c.runSSH(ctx, stdin, args)
	var ae *AgentError
	var hk *HostKeyError
	if err != nil && hadConn && !errors.As(err, &ae) && !errors.As(err, &hk) {
		// the kept-open connection may have gone stale; retry once on a fresh one
		c.dropConn()
		out, err = c.runSSH(ctx, stdin, args)
	}
	return out, err
}

func runLocal(ctx context.Context, cfg Config, stdin []byte, args []string) ([]byte, error) {
	path := cfg.AgentPath
	if path == "" {
		path = "/usr/local/bin/caddyweb-agent"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return stdout.Bytes(), &AgentError{Code: ee.ExitCode(), Stderr: stderr.String()}
	}
	if err != nil {
		return nil, fmt.Errorf("could not run the agent at %s: %w", path, err)
	}
	return stdout.Bytes(), nil
}

func (c *Client) dropConn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) getConn(ctx context.Context) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn, nil
	}
	conn, _, err := dial(ctx, c.cfg, c.signer)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	go func() {
		_ = conn.Wait()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()
	return conn, nil
}

func dial(ctx context.Context, cfg Config, signer ssh.Signer) (*ssh.Client, string, error) {
	if signer == nil {
		return nil, "", errors.New("no SSH key available")
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.User
	if user == "" {
		user = "caddyweb"
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	var seen string
	sc := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = ssh.FingerprintSHA256(key)
			if cfg.HostKey == "" || cfg.HostKey != seen {
				return &HostKeyError{Fingerprint: seen, Expected: cfg.HostKey}
			}
			return nil
		},
		Timeout: 8 * time.Second,
	}
	d := net.Dialer{Timeout: 6 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("cannot reach %s: %w", addr, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, addr, sc)
	if err != nil {
		nc.Close()
		var hk *HostKeyError
		if errors.As(err, &hk) {
			return nil, seen, hk
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, seen, fmt.Errorf("the server at %s refused CaddyWeb's key for user %q. Did you run the agent installer on it? (%v)", addr, user, err)
		}
		return nil, seen, fmt.Errorf("SSH connection to %s failed: %w", addr, err)
	}
	return ssh.NewClient(cc, chans, reqs), seen, nil
}

func (c *Client) runSSH(ctx context.Context, stdin []byte, args []string) ([]byte, error) {
	conn, err := c.getConn(ctx)
	if err != nil {
		return nil, err
	}
	sess, err := conn.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdout, sess.Stderr = &stdout, &stderr
	if stdin != nil {
		sess.Stdin = bytes.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(strings.Join(args, " ")) }()
	select {
	case <-ctx.Done():
		sess.Close()
		return nil, errors.New("the Caddy server took too long to answer")
	case err = <-done:
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return stdout.Bytes(), &AgentError{Code: ee.ExitStatus(), Stderr: stderr.String()}
	}
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// ProbeHostKey connects just far enough to learn the server's key fingerprint.
func ProbeHostKey(ctx context.Context, cfg Config, signer ssh.Signer) (string, error) {
	cfg.HostKey = ""
	_, fp, err := dial(ctx, cfg, signer)
	if fp != "" {
		return fp, nil
	}
	return "", err
}

// LoadOrCreateKey reads the ed25519 key in dir, creating it on first run.
func LoadOrCreateKey(dir string) (ssh.Signer, string, error) {
	path := filepath.Join(dir, "id_ed25519")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return nil, "", gerr
		}
		block, merr := ssh.MarshalPrivateKey(priv, "caddyweb")
		if merr != nil {
			return nil, "", merr
		}
		data = pem.EncodeToMemory(block)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, "", err
		}
		signer, _ := ssh.NewSignerFromKey(priv)
		pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " caddyweb"
		_ = os.WriteFile(path+".pub", []byte(pub+"\n"), 0o644)
	} else if err != nil {
		return nil, "", err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " caddyweb"
	return signer, pub, nil
}

// KeyPath is where the private key lives (used for rsync -e ssh -i).
func KeyPath(dir string) string { return filepath.Join(dir, "id_ed25519") }
