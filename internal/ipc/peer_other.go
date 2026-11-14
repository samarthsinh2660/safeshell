//go:build !linux

package ipc

import (
	"errors"
	"net"
)

func peer(net.Conn) (uid, pid int, err error) {
	return 0, 0, errors.New("peer credentials are only available on Linux")
}
