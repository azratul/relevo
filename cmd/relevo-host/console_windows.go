//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// detachOwnConsole hides the console window when relevo-host was started from
// the Start menu or by double-click: then the console belongs to this process
// alone. Started from a terminal, the console is shared and stays attached.
func detachOwnConsole() {
	k := windows.NewLazySystemDLL("kernel32.dll")
	var list [4]uint32
	n, _, _ := k.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&list[0])), uintptr(len(list)))
	if n == 1 {
		k.NewProc("FreeConsole").Call()
	}
}
