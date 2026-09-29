//go:build !windows

package api

import "syscall"

func hideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

