package main

import (
	"runtime"

	"github.com/azratul/relevo/internal/icon"
)

// trayIcon returns the tray icon (amber while control is paused).
func trayIcon(paused bool) []byte {
	if runtime.GOOS == "windows" {
		return icon.ICO(paused, 16, 32) // Windows wants .ico
	}
	return icon.PNG(32, paused)
}
