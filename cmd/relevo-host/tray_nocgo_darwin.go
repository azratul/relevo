//go:build darwin && !cgo

package main

import (
	"errors"
	"os"

	"github.com/azratul/relevo/internal/hostagent"
)

var errNoTray = errors.New("tray needs a cgo build on macOS")

func runTray(*hostagent.Agent, *hostagent.Admin, chan os.Signal, func(), bool) error {
	return errNoTray
}
