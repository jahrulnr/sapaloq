//go:build !linux

package main

func scheduleInputShape(collapsed bool) {}

// setProgramClass is a Linux/GTK concern (WM_CLASS for taskbar icon matching);
// on other platforms window identity is handled by Wails' Mac/Windows options.
func setProgramClass(string) {}
