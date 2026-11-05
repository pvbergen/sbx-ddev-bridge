//go:build !unix

package main

import "errors"

var errUnsupported = errors.New("background mode is only supported on Linux, WSL2 and macOS; use `ddev-bridge serve`")

func ensure(string) error { return errUnsupported }
func stop(string) error   { return errUnsupported }
func status(string) bool  { return false }
