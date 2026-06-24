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

func isVersionArg(arg string) bool {
	// Note: -V is intentionally NOT lowercased - the lowercase -v is the
	// global --verbose flag.
	if arg == "-V" {
		return true
	}
	switch strings.ToLower(arg) {
	case "version", "--version":
		return true
	default:
		return false
	}
}
