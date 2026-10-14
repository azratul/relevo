//go:build !darwin || cgo

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"fyne.io/systray"

	"relevo/internal/hostagent"
)

var errNoTray = errors.New("no system tray")

// runTray blocks running the tray icon until the user quits.
func runTray(agent *hostagent.Agent, admin *hostagent.Admin, sig chan os.Signal, shutdown func(), openPanel bool) error {
	if err := trayAvailable(); err != nil {
		return errors.Join(errNoTray, err)
	}
	systray.Run(func() {
		systray.SetIcon(trayIcon(false))
		systray.SetTitle("Relevo")
		systray.SetTooltip(T("tooltip"))
		mStatus := systray.AddMenuItem(T("noSession"), "")
		mStatus.Disable()
		mOpen := systray.AddMenuItem(T("openPanel"), T("openPanelTip"))
		mPause := systray.AddMenuItem(T("pause"), "")
		mEnd := systray.AddMenuItem(T("end"), "")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem(T("quit"), T("quitTip"))
		update := func() {
			st := agent.Status()
			if st.Config.Language == "es" {
				lang = "es"
			} else {
				lang = "en"
			}
			switch {
			case st.Session == nil:
				mStatus.SetTitle(T("noSession"))
				mPause.Disable()
				mEnd.Disable()
			case !st.Connected:
				mStatus.SetTitle(T("reconnecting"))
			case st.Paused:
				mStatus.SetTitle(T("paused"))
			default:
				n := 0
				for _, p := range st.Participants {
					if p.Connected && p.State == "enabled" {
						n++
					}
				}
				mStatus.SetTitle(map[bool]string{true: T("ready"), false: T("notReady") + st.Reason}[st.Ready] + T("speakers") + itoa(n))
			}
			if st.Session != nil {
				mPause.Enable()
				mEnd.Enable()
				mPause.SetTitle(map[bool]string{true: T("resume"), false: T("pause")}[st.Paused])
			}
			systray.SetIcon(trayIcon(st.Paused))
		}
		update()
		if openPanel {
			openBrowser(admin.URL())
		}
		go func() {
			tick := time.NewTicker(2 * time.Second)
			for {
				select {
				case <-mOpen.ClickedCh:
					openBrowser(admin.URL())
				case <-mPause.ClickedCh:
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					_ = agent.SetPaused(ctx, !agent.Status().Paused)
					cancel()
				case <-mEnd.ClickedCh:
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					_ = agent.EndSession(ctx)
					cancel()
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				case <-admin.Quit:
					systray.Quit()
					return
				case <-sig:
					systray.Quit()
					return
				case <-agent.Changed():
				case <-tick.C:
				}
				update()
			}
		}()
	}, shutdown)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
