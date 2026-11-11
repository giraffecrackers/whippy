package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"whippy/internal/auth"
	"whippy/internal/github"
	"whippy/internal/settings"
	"whippy/internal/tracker"
)

// app is the state both windows show. It is read and changed on the main
// thread only: by the views, and by the poller's updates through
// mygo.RunOnMain.
type app struct {
	auth   auth.Result
	snap   tracker.Snapshot
	loaded bool
	// err is why the last poll failed, where it did.
	err error

	settings settings.Settings
	tab      int
	newRepo  string
	repoErr  string

	// What the appearance settings edit: the mode's index, and the colors
	// in the wells, set from the settings as each frame builds.
	mode                         int
	accent, background, titleBar ui.Color

	openAtLogin bool
	loginErr    string
	// notifyDenied is set once the system refuses a notification.
	notifyDenied bool

	// What the views ask of the rest of the app. Tests replace these.
	openURL        func(url string)
	refresh        func()
	saveSettings   func()
	setOpenAtLogin func(on bool) error
	setAppearance  func(mode string)
	openDashboard  func()
	hidePopover    func()
	quit           func()
	now            func() time.Time
}

func newApp() *app {
	nop := func() {}
	return &app{
		settings:       settings.Default(),
		openURL:        func(string) {},
		refresh:        nop,
		saveSettings:   nop,
		setOpenAtLogin: func(bool) error { return nil },
		setAppearance:  func(string) {},
		openDashboard:  nop,
		hidePopover:    nop,
		quit:           nop,
		now:            time.Now,
	}
}

func (a *app) apply(u tracker.Update) {
	a.auth, a.snap, a.loaded, a.err = u.Auth, u.Snapshot, u.Loaded, u.Err
}

// wants reports whether the user asked to be told of an event.
func (a *app) wants(ev tracker.Event) bool {
	switch ev.Kind {
	case tracker.ChecksPassed:
		return a.settings.NotifyChecksPassed
	case tracker.RunSucceeded:
		return a.settings.NotifyRunSucceeded
	case tracker.Approved, tracker.ChangesRequested:
		return a.settings.NotifyReviews
	default:
		return true
	}
}

// trayTitle is the text beside the menu bar icon: nothing while all is
// well.
func (a *app) trayTitle() string {
	if a.auth.State != auth.Ready {
		return "!"
	}
	failing, running := a.snap.Counts(a.dismissed)
	var parts []string
	if failing > 0 {
		parts = append(parts, fmt.Sprintf("✕%d", failing))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("●%d", running))
	}
	return strings.Join(parts, " ")
}

func (a *app) trayToolTip() string {
	switch {
	case a.auth.State == auth.GhNotInstalled:
		return "Whippy — GitHub CLI not found"
	case a.auth.State == auth.GhNotLoggedIn:
		return "Whippy — GitHub CLI is not signed in"
	case !a.loaded:
		return "Whippy — loading"
	}
	failing, running := a.snap.Counts(a.dismissed)
	return fmt.Sprintf("Whippy — %d failing, %d running, %d open", failing, running, len(a.snap.PRs))
}

// standaloneRuns are the current runs that no open pull request accounts
// for.
func (a *app) standaloneRuns() []github.Run {
	heads := map[string]bool{}
	for _, pr := range a.snap.PRs {
		heads[pr.Repo+"@"+pr.HeadSHA] = true
	}
	var runs []github.Run
	for _, r := range a.snap.CurrentRuns() {
		if !heads[r.Repo+"@"+r.SHA] {
			runs = append(runs, r)
		}
	}
	return runs
}

// dismissedFor is how long a dismissal is kept: longer than a run stays in
// the snapshot.
const dismissedFor = 72 * time.Hour

func (a *app) dismissed(r github.Run) bool {
	_, ok := a.settings.DismissedRuns[r.ID]
	return ok
}

// dismiss takes a failed run out of what needs attention. It stays in the
// Runs tab.
func (a *app) dismiss(r github.Run) {
	if a.settings.DismissedRuns == nil {
		a.settings.DismissedRuns = map[int64]time.Time{}
	}
	now := a.now()
	for id, at := range a.settings.DismissedRuns {
		if now.Sub(at) > dismissedFor {
			delete(a.settings.DismissedRuns, id)
		}
	}
	a.settings.DismissedRuns[r.ID] = now
	a.saveSettings()
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (a *app) addRepo() {
	repo := strings.TrimSpace(a.newRepo)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimSuffix(repo, "/")
	switch {
	case repo == "":
		return
	case !repoPattern.MatchString(repo):
		a.repoErr = "Write it as owner/name."
		return
	case slices.Contains(a.settings.ExtraRepos, repo):
		a.repoErr = "Already watched."
		return
	}
	a.settings.ExtraRepos = append(a.settings.ExtraRepos, repo)
	slices.Sort(a.settings.ExtraRepos)
	a.newRepo, a.repoErr = "", ""
	a.saveSettings()
}

func (a *app) removeRepo(repo string) {
	a.settings.ExtraRepos = slices.DeleteFunc(a.settings.ExtraRepos, func(r string) bool { return r == repo })
	a.saveSettings()
}

// ago writes how long ago t was, briefly.
func (a *app) ago(t time.Time) string {
	d := a.now().Sub(t)
	switch {
	case t.IsZero():
		return ""
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
