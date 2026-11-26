//go:build unix

package integration_test

import "syscall"

func syscallKill(pid int)error{return syscall.Kill(pid,syscall.SIGKILL)}
