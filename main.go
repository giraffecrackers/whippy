// Whippy is a menu bar app that watches the pull requests you
// opened and the workflow runs you triggered, and tells you when they need
// you.
package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"whippy/internal/auth"
	"whippy/internal/settings"
	"whippy/internal/tracker"
)

// shell ties the app's state to its windows, its tray icon and its poller.
// All of it is used on the main thread.
type shell struct {
	app          *app
	tray         *mygo.Tray
	popover      *mygo.Window
	dashboard    *mygo.Window
	poller       *tracker.Poller
	settingsPath string
	quitting     bool
	asleep       bool
	offline      bool
}

func (s *shell) ready() {
	a := s.app
	var err error
	if s.settingsPath, err = settings.DefaultPath(); err != nil {
		log.Println("settings:", err)
	} else if a.settings, err = settings.Load(s.settingsPath); err != nil {
		log.Println("settings:", err)
	}
	a.openAtLogin = mygo.App.OpenAtLogin()

	s.tray, err = mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        a.trayToolTip(),
	})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	s.popover = mygo.NewWindow(mygo.WindowOptions{
		Title:             "Whippy",
		Width:             420,
		Height:            480,
		Hidden:            true,
		Frameless:         true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		Content:           ui.View(a.popoverView),
	})
	s.dashboard = mygo.NewWindow(mygo.WindowOptions{
		Title:     "Whippy",
		Width:     720,
		Height:    560,
		MinWidth:  480,
		MinHeight: 360,
		// Started at login, the app is its menu bar icon alone.
		Hidden:   mygo.App.WasOpenedAtLogin(),
		StateKey: "dashboard",
		// The view draws the title bar, in the user's color, under the
		// window's buttons.
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 17},
		Content:              ui.View(a.dashboardView),
	})
	// The app is in the Dock while its dashboard shows, and in the menu bar
	// alone otherwise.
	dock := func(show bool) {
		policy := mygo.ActivationPolicyAccessory
		if show {
			policy = mygo.ActivationPolicyRegular
		}
		mygo.App.SetActivationPolicy(policy)
	}
	s.dashboard.OnShow(func() { dock(true) })
	s.dashboard.OnHide(func() { dock(false) })
	dock(!mygo.App.WasOpenedAtLogin())
	s.popover.OnBlur(s.popover.Hide)
	// Closing a window hides it: the app lives in the menu bar. App.Quit
	// must still be able to close them.
	for _, w := range []*mygo.Window{s.popover, s.dashboard} {
		w.OnClose(func(e *mygo.CloseEvent) {
			if !s.quitting {
				e.PreventDefault()
				w.Hide()
			}
		})
	}

	s.poller = tracker.NewPoller(func(u tracker.Update) {
		mygo.RunOnMain(func() { s.apply(u) })
	})
	s.poller.SetExtraRepos(a.settings.ExtraRepos)

	a.openURL = func(url string) {
		if err := mygo.Shell.OpenExternal(url); err != nil {
			log.Println("open:", err)
		}
		s.popover.Hide()
	}
	a.refresh = s.poller.Refresh
	a.saveSettings = s.saveSettings
	a.setOpenAtLogin = mygo.App.SetOpenAtLogin
	a.setAppearance = setAppearance
	setAppearance(a.settings.Appearance)
	a.openDashboard = s.showDashboard
	a.hidePopover = s.popover.Hide
	a.quit = mygo.App.Quit

	if runtime.GOOS == "linux" {
		// AppIndicator only shows menus.
		s.tray.SetMenu(s.trayMenu())
	} else {
		// A menu set on the tray would take the primary click on macOS.
		s.tray.OnClick(s.togglePopover)
		s.tray.OnRightClick(func() {
			s.popover.Hide()
			// Built for this popup, so that Launch at Login shows its
			// current state.
			s.tray.SetMenu(s.trayMenu())
			defer s.tray.SetMenu(nil)
			s.tray.PopUpMenu()
		})
	}

	mygo.App.OnSecondInstance(func([]string, string) { s.showDashboard() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { s.tray.Destroy() })
	mygo.Power.OnSuspend(func() { s.asleep = true; s.pause() })
	mygo.Power.OnResume(func() { s.asleep = false; s.pause() })
	mygo.Network.OnChanged(func(status mygo.NetworkStatus) { s.offline = !status.Online; s.pause() })

	// The poller stops with the process.
	go s.poller.Run(context.Background())
}

// setAppearance makes the app light or dark, or as the system is: its
// views, and the menus and window buttons with them.
func setAppearance(mode string) {
	switch mode {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

func (s *shell) pause() { s.poller.SetPaused(s.asleep || s.offline) }

// apply shows what the poller found and sends its notifications.
func (s *shell) apply(u tracker.Update) {
	a := s.app
	was := a.auth.State
	a.apply(u)
	s.tray.SetTitle(a.trayTitle())
	s.tray.SetToolTip(a.trayToolTip())
	s.popover.Invalidate()
	s.dashboard.Invalidate()

	if a.auth.State != auth.Ready && was == auth.Ready {
		ev := tracker.Event{Key: "auth", Title: "GitHub CLI is not signed in", Body: "Run gh auth login in a terminal to keep watching."}
		if a.auth.State == auth.GhNotInstalled {
			ev.Title, ev.Body = "GitHub CLI not found", "Install gh to let Whippy sign in."
		}
		s.notify(ev)
	}
	for _, ev := range u.Events {
		if a.wants(ev) {
			s.notify(ev)
		}
	}
}

func (s *shell) notify(ev tracker.Event) {
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:       ev.Key,
		Group:    ev.Repo,
		Title:    ev.Title,
		Subtitle: ev.Subtitle,
		Body:     ev.Body,
	})
	n.OnClick(func() {
		if ev.URL != "" {
			mygo.Shell.OpenExternal(ev.URL)
		} else {
			s.showDashboard()
		}
		// macOS keeps a clicked notification until it is closed.
		n.Close()
	})
	// The first Show waits for the user's answer about notifications.
	go func() {
		err := n.Show()
		if err == nil {
			return
		}
		log.Println("notification:", err)
		if errors.Is(err, mygo.ErrNotificationsDenied) {
			mygo.RunOnMain(func() {
				s.app.notifyDenied = true
				s.dashboard.Invalidate()
			})
		}
	}()
}

func (s *shell) saveSettings() {
	s.poller.SetExtraRepos(s.app.settings.ExtraRepos)
	// A dismissed run changes the counts.
	s.tray.SetTitle(s.app.trayTitle())
	s.tray.SetToolTip(s.app.trayToolTip())
	s.popover.Invalidate()
	s.dashboard.Invalidate()
	if s.settingsPath == "" {
		return
	}
	if err := settings.Save(s.settingsPath, s.app.settings); err != nil {
		log.Println("settings:", err)
	}
}

func (s *shell) showDashboard() {
	s.popover.Hide()
	s.dashboard.Show()
	mygo.App.Focus()
	s.dashboard.Focus()
}

func (s *shell) trayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Dashboard", Click: func(*mygo.MenuItem, *mygo.Window) { s.showDashboard() }},
		{Label: "Refresh Now", Click: func(*mygo.MenuItem, *mygo.Window) { s.poller.Refresh() }},
		mygo.Separator(),
		{
			Label: "Launch at Login", Type: mygo.MenuItemCheckbox, Checked: mygo.App.OpenAtLogin(),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				on := !mygo.App.OpenAtLogin()
				if err := mygo.App.SetOpenAtLogin(on); err != nil {
					log.Println("launch at login:", err)
					return
				}
				s.app.openAtLogin = on
				s.dashboard.Invalidate()
			},
		},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

// showPopover places the panel next to the tray icon on its display. macOS
// icons are at the top; a bottom taskbar puts the panel above its icon.
func (s *shell) showPopover() {
	anchor := s.tray.Bounds()
	if anchor.Width == 0 || anchor.Height == 0 {
		// A Windows icon in the notification area's overflow has no bounds.
		s.popover.Center()
	} else {
		area := mygo.Screen.DisplayNearestPoint(mygo.Point{
			X: anchor.X + anchor.Width/2,
			Y: anchor.Y + anchor.Height/2,
		}).WorkArea
		b := s.popover.Bounds()
		x := anchor.X + (anchor.Width-b.Width)/2
		y := anchor.Y + anchor.Height + 6
		if y+b.Height > area.Y+area.Height {
			y = anchor.Y - b.Height - 6
		}
		x = max(area.X, min(x, area.X+max(0, area.Width-b.Width)))
		y = max(area.Y, min(y, area.Y+max(0, area.Height-b.Height)))
		s.popover.SetPosition(x, y)
	}
	s.popover.Show()
	s.popover.Focus()
}

func (s *shell) togglePopover() {
	if s.popover.IsVisible() {
		s.popover.Hide()
	} else {
		s.showPopover()
	}
}

//go:embed resources/tray.png
var trayTemplate []byte

//go:embed resources/tray-color.png
var trayColor []byte

// trayIcon is the whip: black on transparent for macOS to tint, and in
// color elsewhere, where it shows on light and dark taskbars alike. Both
// are drawn by cmd/icons.
func trayIcon() []byte {
	if runtime.GOOS == "darwin" {
		return trayTemplate
	}
	return trayColor
}

func main() {
	// A second launch shows the first one's dashboard.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	s := &shell{app: newApp()}
	mygo.App.SetName("Whippy")
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	// A tray icon does not keep the app running once its windows close;
	// a listener here does.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { s.quitting = true })
	mygo.App.WhenReady(s.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
