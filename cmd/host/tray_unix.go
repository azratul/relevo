//go:build linux || freebsd || openbsd || netbsd

package main

import (
	"errors"

	"github.com/godbus/dbus/v5"
)

// trayAvailable checks for a StatusNotifierItem host; without one the icon
// would be invisible, leaving a process the user cannot close.
func trayAvailable() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has); err != nil {
		return err
	}
	if !has {
		return errors.New("no StatusNotifierWatcher on the session bus")
	}
	return nil
}
