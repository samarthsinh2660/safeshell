package runtime

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listenInodes returns the inode of every socket in the LISTEN state on port in a /proc/net/tcp or tcp6 table.
// Any local address counts: a listener on 0.0.0.0 or :: can answer a connection to 127.0.0.1 as well.
func listenInodes(r io.Reader, port int) []string {
	var inodes []string
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" { // 0A is LISTEN
			continue
		}
		_, p, ok := strings.Cut(f[1], ":")
		if n, err := strconv.ParseUint(p, 16, 16); ok && err == nil && int(n) == port {
			inodes = append(inodes, f[9])
		}
	}
	return inodes
}

// portOwner says whether anything is listening on port and, if so, whether every listener there belongs to pid.
// It reads the tables under proc (normally /proc).
func portOwner(proc string, pid, port int) (listening, owned bool) {
	var inodes []string
	for _, t := range []string{"net/tcp", "net/tcp6"} {
		if f, err := os.Open(filepath.Join(proc, t)); err == nil {
			inodes = append(inodes, listenInodes(f, port)...)
			f.Close()
		}
	}
	if len(inodes) == 0 {
		return false, false
	}
	fdDir := filepath.Join(proc, strconv.Itoa(pid), "fd")
	fds, _ := os.ReadDir(fdDir)
	mine := map[string]bool{}
	for _, fd := range fds {
		if l, err := os.Readlink(filepath.Join(fdDir, fd.Name())); err == nil {
			mine[l] = true
		}
	}
	for _, in := range inodes {
		if !mine[fmt.Sprintf("socket:[%s]", in)] {
			return true, false
		}
	}
	return true, true
}
