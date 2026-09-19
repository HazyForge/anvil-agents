package desktop

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// IsAddrInUse reports whether err is a listen failure caused by the loopback
// port already being bound (a second Anvil Agents Desktop launch while the
// first instance is still running). It matches syscall.EADDRINUSE through any
// wrapping (including *net.OpError) plus the platform error strings: the POSIX
// "address already in use" and the Windows Winsock "Only one usage of each
// socket address...".
func IsAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.EADDRINUSE) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "address already in use") {
		return true
	}
	if strings.Contains(msg, "only one usage of each socket address") {
		return true
	}
	return false
}

// ListenURL returns the loopback HTTP URL for a TCP listen address such as
// "127.0.0.1:1738". Used for the second-launch handoff: instead of exiting
// with a bind error, the new process opens the existing instance in the
// default browser.
func ListenURL(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = defaultListen
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%s/", host, port)
}
