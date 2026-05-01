package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

const refreshInterval = 60 * time.Second

func setupLogging() {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	logDir := filepath.Join(dir, "claude-usage-widget")
	_ = os.MkdirAll(logDir, 0o700)
	f, err := os.OpenFile(filepath.Join(logDir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	setupLogging()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
		}
	}()
	log.Printf("starting up")

	cfg, _, err := LoadConfig()
	if err != nil {
		log.Printf("LoadConfig error: %v -> running setup", err)
		cfg = runSetup(err)
		if cfg == nil {
			log.Printf("setup returned nil; exiting")
			return
		}
	}
	log.Printf("config loaded; running main UI")
	runMain(cfg)
	log.Printf("main UI returned; exiting")
}

type usageRow struct {
	bar      *walk.ProgressBar
	pctLabel *walk.Label
	resetLbl *walk.Label
}

func (r *usageRow) update(u *UsageLimit, missingText string) {
	if u == nil {
		r.bar.SetValue(0)
		r.pctLabel.SetText("—")
		r.resetLbl.SetText(missingText)
		return
	}
	// Utilization is already a 0..100 percent, not a 0..1 fraction.
	pct := int(u.Utilization + 0.5)
	if pct > 100 {
		pct = 100
	} else if pct < 0 {
		pct = 0
	}
	r.bar.SetValue(pct)
	r.pctLabel.SetText(fmt.Sprintf("%d%%", pct))
	if u.ResetsAt != nil {
		r.resetLbl.SetText("Resets " + humanizeDur(*u.ResetsAt))
	} else {
		r.resetLbl.SetText("")
	}
}

func humanizeDur(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("in %dh %dm", h, m)
	}
	return fmt.Sprintf("in %dm", m)
}

func rowComposite(name string, r *usageRow) Widget {
	return Composite{
		Layout: VBox{Margins: Margins{Top: 2, Bottom: 2}, Spacing: 2},
		Children: []Widget{
			Composite{
				Layout: HBox{Margins: Margins{}},
				Children: []Widget{
					Label{Text: name},
					HSpacer{},
					Label{AssignTo: &r.pctLabel, Text: "—"},
				},
			},
			ProgressBar{AssignTo: &r.bar, MinValue: 0, MaxValue: 100},
			Label{AssignTo: &r.resetLbl, Text: "—"},
		},
	}
}

func runMain(cfg *Config) {
	client := NewClient(cfg.SessionKey, cfg.OrgID)

	var mw *walk.MainWindow
	var statusLbl *walk.Label

	session := &usageRow{}
	allModels := &usageRow{}
	sonnet := &usageRow{}
	design := &usageRow{}
	extra := &usageRow{}

	var inFlight atomic.Bool
	var ni *walk.NotifyIcon

	updateTrayTooltip := func(d *UsageData) {
		if ni == nil {
			return
		}
		summary := fmt.Sprintf("Session %d%% • Weekly %d%%",
			int(d.FiveHour.Utilization+0.5),
			int(d.SevenDay.Utilization+0.5))
		_ = ni.SetToolTip(summary)
	}

	doRefresh := func() {
		if !inFlight.CompareAndSwap(false, true) {
			return
		}
		statusLbl.SetText("Refreshing…")
		go func() {
			defer inFlight.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			data, _, err := client.FetchUsage(ctx)
			mw.Synchronize(func() {
				if err != nil {
					statusLbl.SetText("Error: " + err.Error())
					return
				}
				if newKey := client.SessionKey(); newKey != cfg.SessionKey {
					cfg.SessionKey = newKey
					_ = SaveConfig(cfg)
				}
				session.update(&data.FiveHour, "—")
				allModels.update(&data.SevenDay, "—")
				sonnet.update(data.SevenDaySonnet, "no data")
				design.update(data.IguanaNecktie, "not used")
				extra.update(data.ExtraUsage, "none")
				statusLbl.SetText("Updated " + time.Now().Format("15:04:05"))
				updateTrayTooltip(data)
			})
		}()
	}

	openSettings := func() {
		newCfg := editConfigDialog(mw, cfg)
		if newCfg != nil {
			cfg.SessionKey = newCfg.SessionKey
			cfg.OrgID = newCfg.OrgID
			client.SetCredentials(cfg.SessionKey, cfg.OrgID)
			doRefresh()
		}
	}

	showWindow := func() {
		mw.Show()
		win.SetForegroundWindow(mw.Handle())
	}

	mwDecl := MainWindow{
		AssignTo: &mw,
		Title:    "Claude Usage",
		Size:     Size{Width: 320, Height: 500},
		Layout:   VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
		Children: []Widget{
			Label{Text: "Plan usage", Font: Font{Bold: true, PointSize: 10}},
			rowComposite("Current session", session),
			Label{Text: "Weekly limits", Font: Font{Bold: true, PointSize: 10}},
			rowComposite("All models", allModels),
			rowComposite("Sonnet only", sonnet),
			rowComposite("Claude Design", design),
			Label{Text: "Extra usage", Font: Font{Bold: true, PointSize: 10}},
			rowComposite("Extra", extra),
			VSpacer{},
			Composite{
				Layout: HBox{Margins: Margins{}},
				Children: []Widget{
					Label{AssignTo: &statusLbl, Text: "Loading…"},
					HSpacer{},
					PushButton{Text: "Settings", OnClicked: openSettings},
					PushButton{Text: "Refresh", OnClicked: doRefresh},
				},
			},
		},
	}

	if err := mwDecl.Create(); err != nil {
		log.Fatal(err)
	}

	win.SetWindowPos(mw.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if reason == walk.CloseReasonUser {
			*canceled = true
			mw.Hide()
		}
	})

	if err := installTrayIcon(mw, &ni, showWindow, doRefresh, openSettings); err != nil {
		log.Printf("notify icon: %v", err)
	}
	defer func() {
		if ni != nil {
			ni.Dispose()
		}
	}()

	doRefresh()

	stopPoll := make(chan struct{})
	var pollWG sync.WaitGroup
	pollWG.Add(1)
	go func() {
		defer pollWG.Done()
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				doRefresh()
			case <-stopPoll:
				return
			}
		}
	}()

	mw.Run()
	close(stopPoll)
	pollWG.Wait()
}

func runSetup(loadErr error) *Config {
	var mw *walk.MainWindow
	var sessionKeyEdit, orgIDEdit *walk.LineEdit
	var statusLbl *walk.Label
	var saved *Config

	msg := "Open claude.ai → DevTools (F12) → Application → Cookies → claude.ai\n" +
		"Copy 'sessionKey' and 'lastActiveOrg' values, paste below."
	if loadErr != nil {
		msg = "Setup needed (" + loadErr.Error() + ")\n\n" + msg
	}

	mwDecl := MainWindow{
		AssignTo: &mw,
		Title:    "Claude Usage — setup",
		Size:     Size{Width: 500, Height: 320},
		Layout:   VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
		Children: []Widget{
			Label{Text: msg},
			Label{Text: "sessionKey (sk-ant-sid01-...)"},
			LineEdit{AssignTo: &sessionKeyEdit, PasswordMode: true},
			Label{Text: "Organization ID (UUID, from lastActiveOrg cookie)"},
			LineEdit{AssignTo: &orgIDEdit},
			Label{AssignTo: &statusLbl, Text: ""},
			VSpacer{},
			Composite{
				Layout: HBox{Margins: Margins{}},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text: "Save",
						OnClicked: func() {
							c := &Config{
								SessionKey: strings.TrimSpace(sessionKeyEdit.Text()),
								OrgID:      strings.TrimSpace(orgIDEdit.Text()),
							}
							if c.SessionKey == "" || c.OrgID == "" {
								statusLbl.SetText("Both fields are required.")
								return
							}
							if err := SaveConfig(c); err != nil {
								statusLbl.SetText("Save failed: " + err.Error())
								return
							}
							saved = c
							mw.Close()
						},
					},
					PushButton{
						Text:      "Cancel",
						OnClicked: func() { mw.Close() },
					},
				},
			},
		},
	}

	if err := mwDecl.Create(); err != nil {
		log.Printf("setup window create failed: %v", err)
		return nil
	}
	mw.Run()
	return saved
}

func editConfigDialog(owner walk.Form, current *Config) *Config {
	var dlg *walk.Dialog
	var sessionKeyEdit, orgIDEdit *walk.LineEdit
	var statusLbl *walk.Label
	var saved *Config

	dlgDecl := Dialog{
		AssignTo:      &dlg,
		Title:         "Update Claude credentials",
		MinSize:       Size{Width: 500, Height: 280},
		DefaultButton: nil,
		CancelButton:  nil,
		Layout:        VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
		Children: []Widget{
			Label{Text: "Paste a fresh sessionKey from claude.ai cookies."},
			Label{Text: "sessionKey"},
			LineEdit{AssignTo: &sessionKeyEdit, PasswordMode: true, Text: current.SessionKey},
			Label{Text: "Organization ID"},
			LineEdit{AssignTo: &orgIDEdit, Text: current.OrgID},
			Label{AssignTo: &statusLbl, Text: ""},
			VSpacer{},
			Composite{
				Layout: HBox{Margins: Margins{}},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text: "Save",
						OnClicked: func() {
							c := &Config{
								SessionKey: strings.TrimSpace(sessionKeyEdit.Text()),
								OrgID:      strings.TrimSpace(orgIDEdit.Text()),
							}
							if c.SessionKey == "" || c.OrgID == "" {
								statusLbl.SetText("Both fields are required.")
								return
							}
							if err := SaveConfig(c); err != nil {
								statusLbl.SetText("Save failed: " + err.Error())
								return
							}
							saved = c
							dlg.Accept()
						},
					},
					PushButton{
						Text:      "Cancel",
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}

	if _, err := dlgDecl.Run(owner); err != nil {
		log.Printf("settings dialog error: %v", err)
		return nil
	}
	return saved
}

func installTrayIcon(mw *walk.MainWindow, out **walk.NotifyIcon, onShow, onRefresh, onSettings func()) error {
	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		return err
	}
	if err := ni.SetIcon(walk.IconApplication()); err != nil {
		return err
	}
	if err := ni.SetToolTip("Claude Usage"); err != nil {
		return err
	}

	ni.MouseDown().Attach(func(_, _ int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		if mw.Visible() {
			mw.Hide()
		} else {
			onShow()
		}
	})

	addAction := func(text string, fn func()) error {
		a := walk.NewAction()
		if err := a.SetText(text); err != nil {
			return err
		}
		a.Triggered().Attach(fn)
		return ni.ContextMenu().Actions().Add(a)
	}

	if err := addAction("Show", onShow); err != nil {
		return err
	}
	if err := addAction("Refresh", onRefresh); err != nil {
		return err
	}
	if err := addAction("Settings…", onSettings); err != nil {
		return err
	}
	if err := ni.ContextMenu().Actions().Add(walk.NewSeparatorAction()); err != nil {
		return err
	}
	if err := addAction("Exit", func() { walk.App().Exit(0) }); err != nil {
		return err
	}

	if err := ni.SetVisible(true); err != nil {
		return err
	}
	*out = ni
	return nil
}
