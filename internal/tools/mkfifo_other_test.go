//go:build !unix

package tools

import "errors"

func mkfifo(string) error { return errors.New("no FIFOs on this platform") }
