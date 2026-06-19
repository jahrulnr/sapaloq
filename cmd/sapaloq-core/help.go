package main

import "strings"

func isHelpArg(arg string) bool {
	switch strings.ToLower(arg) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}
