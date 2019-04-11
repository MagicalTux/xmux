package xmux

import (
	"io"
	"log"
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

	nc, _ := c.Dial("tcp", "hello")
	b := make([]byte, 4096)
	n, _ := nc.Read(b)

	if string(b[:n]) != "hello world" {
		t.Errorf("failed to read hello world, received %v", b[:n])
	}

	nc, _ = c.Dial("tcp", "100MB")
	b = make([]byte, 1024*1024)
	totN := 0

	for {
		n, err := nc.Read(b)
		totN += n
		if err != nil {
			if err != io.EOF {
				t.Errorf("error from peer in 100MB test: %s", err)
			}
			break
		}
	}

	nc.Close()

	if totN != 100*1024*1024 {
		t.Errorf("100MB test didn't return 100 MB")
	}

	// echo test
	nc, _ = c.Dial("tcp", "echo")
	b = make([]byte, 4096)

	nc.Write([]byte("This is a test"))
	n, _ = nc.Read(b)

	if string(b[:n]) != "This is a test" {
		t.Errorf("echo test failed, received %v", b[:n])
	}
}

func srvTest(t *testing.T, s *Session) {
	for {
		c, err := s.AcceptChannel()
		if err != nil {
			log.Printf("server: bye")
			return
		}

		go func(c *Channel) {
			_, tgt := c.Endpoint()
			switch tgt {
			case "hello":
				c.Write([]byte("hello world"))
			case "100MB":
				buf := make([]byte, 10*1024*1024) // 10MB
				for i := 0; i < 10; i++ {
					c.Write(buf)
				}
			case "echo":
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if err != nil {
						break
					}
					c.Write(buf[:n])
				}
			}
			c.Close()
		}(c)
	}
}
