package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A second launch (e.g. clicking Relevo again in the app menu) must not start
// another host with another tray icon: it opens the running one's panel.
// The panel URL contains its key, so the file is private to the user.

type instanceInfo struct {
	PID   int    `json:"pid"`
	Panel string `json:"panel"`
}

func instanceFile(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "instance.json")
}

// runningPanel returns the panel URL of a live instance, if any.
func runningPanel(configPath string) string {
	b, err := os.ReadFile(instanceFile(configPath))
	if err != nil {
		return ""
	}
	var in instanceInfo
	if json.Unmarshal(b, &in) != nil || !strings.HasPrefix(in.Panel, "http://127.0.0.1:") {
		return ""
	}
	base, _, _ := strings.Cut(in.Panel, "#")
	c := &http.Client{Timeout: time.Second}
	res, err := c.Get(base)
	if err != nil {
		return ""
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	return in.Panel
}

func writeInstance(configPath, panel string) func() {
	f := instanceFile(configPath)
	_ = os.MkdirAll(filepath.Dir(f), 0o700)
	b, _ := json.Marshal(instanceInfo{PID: os.Getpid(), Panel: panel})
	_ = os.WriteFile(f, b, 0o600)
	return func() {
		// remove only our own record
		if cur, err := os.ReadFile(f); err == nil && string(cur) == string(b) {
			_ = os.Remove(f)
		}
	}
}
