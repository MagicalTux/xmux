package xmux

import (
	"net"
	"os"
	"syscall"
	"testing"
)

// use smux for tests
func unixPair(typ int) (*net.UnixConn, *net.UnixConn) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, typ, 0)
	if err != nil {
		panic(os.NewSyscallError("socketpair", err))
	}

	file := os.NewFile(uintptr(fds[0]), "")
	c0, err := net.FileConn(file)
	if err != nil {
		panic(err)
	}
	file.Close()

	file = os.NewFile(uintptr(fds[1]), "")
	c1, err := net.FileConn(file)
	if err != nil {
		panic(err)
	}
	file.Close()

	return c0.(*net.UnixConn), c1.(*net.UnixConn)
}

func TestXmux(t *testing.T) {
	c0, c1 := unixPair(syscall.SOCK_STREAM)

	s := New(c0, true)
	c := New(c1, false)

	go srvTest(t, s)

	_ = c
}

func srvTest(t *testing.T, s *Session) {
	for {
		c, err := s.Accept()
		if err != nil {
			return
		}

		go func(c net.Conn) {
			c.Write([]byte("hello world"))
			c.Close()
		}(c)
	}
}
