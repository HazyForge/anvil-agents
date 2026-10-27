//go:build !linux

package host

import (
	"fmt"
	"net"
)

func listenControl(string) (net.Listener, error) {
	return nil, fmt.Errorf("onechat control sockets require Linux; run the daemon inside WSL")
}

func checkPeer(net.Conn) error {
	return fmt.Errorf("onechat control sockets require Linux; run the daemon inside WSL")
}
