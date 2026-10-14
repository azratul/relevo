//go:build windows || (darwin && cgo)

package main

func trayAvailable() error { return nil }
