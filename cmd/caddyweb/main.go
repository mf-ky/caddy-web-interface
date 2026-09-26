// Command caddyweb is the CaddyWeb server and its admin command line.
//
//	caddyweb serve                      run the web UI (default :8090)
//	caddyweb user list                  list accounts
//	caddyweb user add NAME [--role R]   create an account (admin, power, viewer)
//	caddyweb user passwd NAME           reset a password (forgotten password)
//	caddyweb user role NAME ROLE        change a role
//	caddyweb user delete NAME           remove an account
//	caddyweb pubkey                     print CaddyWeb's SSH public key
//	caddyweb installer                  print the agent installer script
//	caddyweb version
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/mf-ky/caddy-web-interface/agent"
	"github.com/mf-ky/caddy-web-interface/internal/server"
	"github.com/mf-ky/caddy-web-interface/internal/store"
	"github.com/mf-ky/caddy-web-interface/web"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string { return env("CADDYWEB_DATA", "/var/lib/caddyweb") }

func usage() {
	fmt.Fprint(os.Stderr, `CaddyWeb — front end manager for Caddy Server

Usage:
  caddyweb serve [--listen :8090] [--data DIR]
  caddyweb user list
  caddyweb user add NAME [--role admin|power|viewer] [--password PW]
  caddyweb user passwd NAME [--password PW]     (reset a forgotten password)
  caddyweb user role NAME admin|power|viewer
  caddyweb user delete NAME
  caddyweb pubkey                                (CaddyWeb's SSH public key)
  caddyweb installer > install-agent.sh          (agent installer for the Caddy server)
  caddyweb version

All commands accept --data DIR (default $CADDYWEB_DATA or /var/lib/caddyweb).
Roles: admin = full access, power = can add new cards only, viewer = read only.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "user", "users":
		err = cmdUser(os.Args[2:])
	case "pubkey":
		err = cmdPubkey(os.Args[2:], false)
	case "installer":
		err = cmdPubkey(os.Args[2:], true)
	case "version", "--version", "-v":
		fmt.Println("caddyweb", version)
	case "help", "--help", "-h":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", env("CADDYWEB_LISTEN", ":8090"), "address to listen on")
	data := fs.String("data", defaultDataDir(), "data directory")
	certFile := fs.String("tls-cert", env("CADDYWEB_TLS_CERT", ""), "optional TLS certificate file")
	keyFile := fs.String("tls-key", env("CADDYWEB_TLS_KEY", ""), "optional TLS key file")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	srv, err := server.New(server.Options{DataDir: *data, Version: version, Static: web.Static()})
	if err != nil {
		return err
	}
	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}()
	users, _ := store.OpenUsers(*data)
	log.Printf("CaddyWeb %s listening on %s (data: %s)", version, *listen, *data)
	if users != nil && users.Count() == 0 {
		log.Printf("No accounts yet: open the web UI to create the first admin, or run `caddyweb user add NAME --role admin`.")
	}
	if *certFile != "" {
		err = hs.ListenAndServeTLS(*certFile, *keyFile)
	} else {
		err = hs.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// splitFlags lets flags appear after positional arguments (user add bob --role admin).
func splitFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func cmdUser(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing user command")
	}
	sub := args[0]
	fs := flag.NewFlagSet("user "+sub, flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "data directory")
	role := fs.String("role", "admin", "role: admin, power or viewer")
	password := fs.String("password", "", "password (prompted if omitted)")
	pos, err := splitFlags(fs, args[1:])
	if err != nil {
		return err
	}
	if _, err := os.Stat(*data); err != nil {
		return fmt.Errorf("data directory %s not found (use --data or CADDYWEB_DATA): %w", *data, err)
	}
	users, err := store.OpenUsers(*data)
	if err != nil {
		return err
	}
	need := func(n int, what string) error {
		if len(pos) < n {
			return fmt.Errorf("usage: caddyweb user %s %s", sub, what)
		}
		return nil
	}
	switch sub {
	case "list", "ls":
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "USERNAME\tROLE\tLAST LOGIN")
		for _, u := range users.List() {
			last := "never"
			if !u.LastLogin.IsZero() {
				last = u.LastLogin.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", u.Username, store.RoleLabel(u.Role), last)
		}
		return tw.Flush()
	case "add", "create":
		if err := need(1, "NAME [--role admin|power|viewer]"); err != nil {
			return err
		}
		pw, generated, err := getPassword(*password)
		if err != nil {
			return err
		}
		if err := users.Add(pos[0], store.Role(*role), pw); err != nil {
			return err
		}
		fmt.Printf("Created %s (%s).\n", pos[0], store.RoleLabel(store.Role(*role)))
		if generated {
			fmt.Printf("Password: %s\n", pw)
		}
		return nil
	case "passwd", "password", "reset-password", "reset":
		if err := need(1, "NAME"); err != nil {
			return err
		}
		if users.Get(pos[0]) == nil {
			return fmt.Errorf("no user %q (see `caddyweb user list`)", pos[0])
		}
		pw, generated, err := getPassword(*password)
		if err != nil {
			return err
		}
		if err := users.SetPassword(pos[0], pw); err != nil {
			return err
		}
		fmt.Printf("Password for %s changed. Existing sessions for this user are logged out.\n", pos[0])
		if generated {
			fmt.Printf("New password: %s\n", pw)
		}
		return nil
	case "role":
		if err := need(2, "NAME admin|power|viewer"); err != nil {
			return err
		}
		if err := users.SetRole(pos[0], store.Role(pos[1])); err != nil {
			return err
		}
		fmt.Printf("%s is now %s.\n", pos[0], store.RoleLabel(store.Role(pos[1])))
		return nil
	case "delete", "del", "rm", "remove":
		if err := need(1, "NAME"); err != nil {
			return err
		}
		if err := users.Delete(pos[0]); err != nil {
			return err
		}
		fmt.Printf("Deleted %s.\n", pos[0])
		return nil
	}
	usage()
	return fmt.Errorf("unknown user command %q", sub)
}

// getPassword uses the flag, prompts on a terminal, or generates one.
func getPassword(flagPW string) (string, bool, error) {
	if flagPW != "" {
		return flagPW, false, nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("New password (leave empty to generate one): ")
		a, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", false, err
		}
		if len(a) == 0 {
			return randomPassword(), true, nil
		}
		fmt.Print("Repeat password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", false, err
		}
		if string(a) != string(b) {
			return "", false, errors.New("passwords do not match")
		}
		return string(a), false, nil
	}
	// piped input: first line is the password
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return randomPassword(), true, nil
	}
	return line, false, nil
}

func randomPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

func cmdPubkey(args []string, installer bool) error {
	fs := flag.NewFlagSet("pubkey", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "data directory")
	_ = fs.Parse(args)
	b, err := os.ReadFile(filepath.Join(*data, "ssh", "id_ed25519.pub"))
	if err != nil {
		return fmt.Errorf("no key yet — start CaddyWeb once (caddyweb serve) to create it: %w", err)
	}
	if installer {
		fmt.Print(agent.Installer(string(b)))
		return nil
	}
	fmt.Print(string(b))
	return nil
}
