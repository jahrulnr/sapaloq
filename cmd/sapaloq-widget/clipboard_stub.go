//go:build !linux

package main

func clipboardGetImageLinux() (*clipboardImage, error) {
	return nil, nil
}
