package adapter

import (
	"fmt"
	"os"
	"runtime"
	"slices"
)

// BackendInfo describes a registered key backend.
type BackendInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"` // tested | compiles | experimental (see docs/platform-support.md)
}

type backendEntry struct {
	info BackendInfo
	new  func() (KeyBackend, error)
}

var backends = map[string]backendEntry{}

// Register adds a backend (called from init in platform files).
func Register(info BackendInfo, ctor func() (KeyBackend, error)) {
	backends[info.Name] = backendEntry{info, ctor}
}

func init() {
	Register(BackendInfo{Name: "sim", Description: "Simulated: records keys without injecting them", Status: "tested"},
		func() (KeyBackend, error) { return NewSim(), nil })
}

// Backends lists the backends compiled into this binary.
func Backends() []BackendInfo {
	var out []BackendInfo
	for _, b := range backends {
		out = append(out, b.info)
	}
	slices.SortFunc(out, func(a, b BackendInfo) int {
		if a.Name < b.Name {
			return -1
		}
		return 1
	})
	return out
}

// NewBackend creates a backend by name; "auto" picks one for this session.
func NewBackend(name string) (KeyBackend, error) {
	if name == "" || name == "auto" {
		name = AutoBackend()
	}
	b, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("backend %q not available on %s", name, runtime.GOOS)
	}
	return b.new()
}

// AutoBackend chooses a backend from the environment.
func AutoBackend() string {
	has := func(n string) bool { _, ok := backends[n]; return ok }
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "darwin"
	}
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" && has("hyprland"):
		return "hyprland"
	case os.Getenv("WAYLAND_DISPLAY") != "" && has("portal"):
		return "portal"
	case os.Getenv("DISPLAY") != "" && has("x11"):
		return "x11"
	}
	return "sim"
}
