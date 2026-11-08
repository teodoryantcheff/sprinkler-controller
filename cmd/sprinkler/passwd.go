package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/term"

	"sprinkler/internal/api"
	"sprinkler/internal/store"
)

// passwdCmd sets a user's password directly in the database, for when the
// old one is lost. It also logs out every session.
//
//	sudo -u sprinkler sprinkler passwd -config /etc/sprinkler/sprinkler.toml
//	echo 'new password' | sprinkler passwd -db dev.db
func passwdCmd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	cfgPath := fs.String("config", "", "TOML config to read the database path from")
	dbPath := fs.String("db", "", "database file (overrides -config)")
	user := fs.String("user", "admin", "user name")
	keepSessions := fs.Bool("keep-sessions", false, "don't log out existing sessions")
	fs.Parse(args)

	path := *dbPath
	if path == "" && *cfgPath != "" {
		cfg := defaultConfig()
		if _, err := toml.DecodeFile(*cfgPath, &cfg); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		path = cfg.DB
	}
	if path == "" {
		return errors.New("give -config or -db")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	pw, err := readNewPassword(os.Stdin, os.Stderr)
	if err != nil {
		return err
	}
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := resetPassword(st, *user, pw, !*keepSessions); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Password for %q set in %s.", *user, path)
	if !*keepSessions {
		fmt.Fprint(os.Stderr, " All sessions are logged out.")
	}
	fmt.Fprintln(os.Stderr, " No restart needed.")
	return nil
}

// resetPassword stores the new password hash and, with logoutAll, rotates
// the session secret so every existing login cookie stops working.
func resetPassword(st *store.Store, user, pw string, logoutAll bool) error {
	if len([]rune(pw)) < api.MinPassword {
		return fmt.Errorf("password must be at least %d characters", api.MinPassword)
	}
	h, err := api.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.SaveUser(user, h); err != nil {
		return err
	}
	if logoutAll {
		// An empty secret makes the server generate a new one on the next request.
		return st.SetRawSetting("session_secret", "")
	}
	return nil
}

// readNewPassword asks twice without echo on a terminal, or reads one line
// from a pipe.
func readNewPassword(in *os.File, prompt io.Writer) (string, error) {
	if !term.IsTerminal(int(in.Fd())) {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(prompt, "New password: ")
	a, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(prompt)
	if err != nil {
		return "", err
	}
	fmt.Fprint(prompt, "Repeat: ")
	b, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(prompt)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errors.New("the passwords differ")
	}
	return string(a), nil
}
