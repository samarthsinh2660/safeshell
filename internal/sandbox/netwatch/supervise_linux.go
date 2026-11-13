//go:build linux && (amd64 || arm64)

package netwatch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

const (
	ioctlRecv    = 0xC0502100 // SECCOMP_IOCTL_NOTIF_RECV
	ioctlSend    = 0xC0182101 // SECCOMP_IOCTL_NOTIF_SEND
	flagContinue = 1          // SECCOMP_USER_NOTIF_FLAG_CONTINUE
)

type notif struct {
	ID    uint64
	PID   uint32
	Flags uint32
	NR    int32
	Arch  uint32
	IP    uint64
	Args  [6]uint64
}

type notifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// Supervise answers notifications on fd until every filtered process has
// exited, and returns what they tried to reach.
func Supervise(fd *os.File) []sandbox.NetAttempt {
	defer fd.Close()
	var seen []sandbox.NetAttempt
	for {
		var n notif
		if err := ioctl(fd, ioctlRecv, unsafe.Pointer(&n)); err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return sandbox.MergeAttempts(seen) // ENOENT: no filtered process is left
		}
		att, errno := judge(n)
		if att != nil {
			seen = append(seen, *att)
		}
		resp := notifResp{ID: n.ID}
		if errno == 0 {
			resp.Flags = flagContinue
		} else {
			resp.Error = -int32(errno)
		}
		_ = ioctl(fd, ioctlSend, unsafe.Pointer(&resp)) // fails only if the caller died meanwhile
	}
}

// judge decides one notification. A call that names no destination goes on
// unchanged; anything else is refused. sendmmsg carries several messages and
// every one is checked.
func judge(n notif) (*sandbox.NetAttempt, syscall.Errno) {
	mem, err := os.Open(fmt.Sprintf("/proc/%d/mem", n.PID))
	if err != nil {
		return nil, unix.EACCES // cannot see where it is going, so do not let it go
	}
	defer mem.Close()
	read := func(addr uint64, size int) []byte {
		if size <= 0 || size > 4096 {
			return nil
		}
		b := make([]byte, size)
		got, _ := mem.ReadAt(b, int64(addr))
		return b[:got]
	}
	a := n.Args
	call := map[int32]string{unix.SYS_CONNECT: "connect", unix.SYS_SENDTO: "sendto", unix.SYS_SENDMSG: "sendmsg", unix.SYS_SENDMMSG: "sendmmsg"}[n.NR]
	// addrlen and vlen are 32-bit in the kernel; the upper register bits are not ours to trust.
	switch n.NR {
	case unix.SYS_CONNECT:
		return destination(read, call, a[1], int(uint32(a[2])), nil)
	case unix.SYS_SENDTO:
		var data []byte
		if a[4] != 0 && a[2] > 0 {
			data = read(a[1], int(min(a[2], 512)))
		}
		return destination(read, call, a[4], int(uint32(a[5])), data)
	case unix.SYS_SENDMSG:
		return fromMsghdr(read, call, a[1])
	default: // sendmmsg: an array of {msghdr (56 bytes), msg_len, padding} = 64 bytes each
		vlen := min(int(uint32(a[2])), 64)
		var first *sandbox.NetAttempt
		for i := 0; i < vlen; i++ {
			att, errno := fromMsghdr(read, call, a[1]+uint64(i)*64)
			if errno != 0 {
				return att, errno
			}
			if first == nil {
				first = att
			}
		}
		return first, 0
	}
}

// fromMsghdr reads the destination named in a struct msghdr at addr.
func fromMsghdr(read func(uint64, int) []byte, call string, addr uint64) (*sandbox.NetAttempt, syscall.Errno) {
	h := read(addr, 16)
	if len(h) < 16 {
		return nil, unix.EACCES
	}
	return destination(read, call, binary.LittleEndian.Uint64(h), int(binary.LittleEndian.Uint32(h[8:])), nil)
}

func destination(read func(uint64, int) []byte, call string, name uint64, nameLen int, data []byte) (*sandbox.NetAttempt, syscall.Errno) {
	if name == 0 {
		return nil, 0
	}
	att, ok := sandbox.ParseSockaddr(read(name, min(nameLen, 128)))
	if !ok {
		return &sandbox.NetAttempt{Syscall: call, Family: "other"}, unix.EAFNOSUPPORT
	}
	att.Syscall = call
	if att.Port == 53 {
		att.DNS = sandbox.ParseDNSName(data)
	}
	if att.Family == "unix" {
		return &att, unix.EACCES // a host socket (docker, ssh-agent, dbus) is a way out of the sandbox
	}
	return &att, unix.ENETUNREACH
}
