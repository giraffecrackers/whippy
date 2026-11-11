// Package auth finds a GitHub token by asking the gh CLI for the one it
// already holds. The app keeps no credentials of its own.
package auth

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// State says whether the app can talk to GitHub, and if not, what the user
// has to fix.
type State int

const (
	Ready State = iota
	GhNotInstalled
	GhNotLoggedIn
)

type Result struct {
	State State
	Token string
	// Detail is what gh printed when it had no token, for the setup screen.
	Detail string
}

// An app started from Finder gets launchd's PATH, which has neither of
// Homebrew's directories.
var fallbackPaths = []string{
	"/opt/homebrew/bin/gh",
	"/usr/local/bin/gh",
	"/usr/bin/gh",
	"/home/linuxbrew/.linuxbrew/bin/gh",
}

// Resolver is replaced in tests.
type Resolver struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Stat     func(string) error
	Run      func(ctx context.Context, bin string, args ...string) (stdout, stderr string, err error)
}

var Default = Resolver{
	Getenv:   os.Getenv,
	LookPath: exec.LookPath,
	Stat:     func(p string) error { _, err := os.Stat(p); return err },
	Run: func(ctx context.Context, bin string, args ...string) (string, string, error) {
		var out, errb strings.Builder
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		return out.String(), errb.String(), err
	},
}

func Resolve(ctx context.Context) Result { return Default.Resolve(ctx) }

func (r Resolver) Resolve(ctx context.Context) Result {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if tok := strings.TrimSpace(r.Getenv(name)); tok != "" {
			return Result{State: Ready, Token: tok}
		}
	}
	bin, err := r.ghPath()
	if err != nil {
		return Result{State: GhNotInstalled}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, stderr, err := r.Run(ctx, bin, "auth", "token", "--hostname", "github.com")
	tok := strings.TrimSpace(out)
	if err != nil || tok == "" {
		return Result{State: GhNotLoggedIn, Detail: firstLine(stderr)}
	}
	return Result{State: Ready, Token: tok}
}

func (r Resolver) ghPath() (string, error) {
	if p, err := r.LookPath("gh"); err == nil {
		return p, nil
	}
	for _, p := range fallbackPaths {
		if r.Stat(p) == nil {
			return p, nil
		}
	}
	return "", errors.New("gh not found")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
