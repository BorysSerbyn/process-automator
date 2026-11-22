package sysenv

import (
	"context"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/BorysSerbyn/process-automator/core/internal/product"
)

// KeepAwake holds off the computer sleeping on its own until release is
// called, so a run isn't cut off halfway, as far as logind lets the scheduler:
// a sleep inhibitor (what systemd-inhibit takes) needs a login session or an
// administrator's polkit rule, and the scheduler, a user service, has neither;
// an idle inhibitor it may take, which logind's own idle action honours (a
// desktop's idle suspend may not). The lock is a file descriptor, so it can't
// outlive a crash. Without logind, nothing is held.
func KeepAwake() (release func()) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return func() {}
	}
	defer func() { _ = conn.Close() }() // the lock is the descriptor, not the connection
	for _, what := range []string{"sleep:idle", "idle"} {
		if fd, err := inhibit(conn, what); err == nil {
			return func() { _ = syscall.Close(int(fd)) }
		}
	}
	return func() {}
}

func inhibit(conn *dbus.Conn, what string) (dbus.UnixFD, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var fd dbus.UnixFD
	err := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		CallWithContext(ctx, "org.freedesktop.login1.Manager.Inhibit", 0,
			what, product.Name, "running a routine", "block").Store(&fd)
	return fd, err
}

// FullyAwake is whether the computer is awake for work. On Linux it always is
// when this runs: while suspended, nothing outside the kernel runs, and
// Linux has no maintenance wakes that run apps.
func FullyAwake() bool { return true }
