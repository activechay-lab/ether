//go:build !linux

package main

import "errors"

func runDropCache([]string) error { return errors.New("dropcache runs on Linux only") }
