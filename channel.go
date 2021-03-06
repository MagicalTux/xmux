package xmux

import (
	"bytes"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Channel struct {
	s        *Session
	ch       uint32
	ep       []byte
	cl       chan struct{} // close chan
	accepted bool

	bufIn   []byte
	inLock  sync.Mutex
	inCond  *sync.Cond
	outLock sync.Mutex

	laddr, raddr net.Addr

	closed uint32
	err    error

	readDeadline  time.Time
	writeDeadline time.Time
	setReadDl     chan time.Time
	setWriteDl    chan time.Time
	timeoutH      sync.Once
}

func (s *Session) newChannel(ch uint32, ep []byte) *Channel {
	res := &Channel{
		s:  s,
		ch: ch,
		ep: ep,
		cl: make(chan struct{}),

		setReadDl:  make(chan time.Time),
		setWriteDl: make(chan time.Time),
	}

	res.inCond = sync.NewCond(&res.inLock)

	return res
}

// processTimeout is a goroutine that will wake channel locks upon reaching any timeout
func (ch *Channel) processTimeout() {
	var readDl, writeDl <-chan time.Time
	var readDlT, writeDlT *time.Timer

	for {
		select {
		case <-ch.cl:
			return // channel closed
		case t := <-ch.setReadDl:
			if readDlT != nil {
				readDlT.Stop()
				readDlT = nil
				readDl = nil
			}
			ch.readDeadline = t
			if !t.IsZero() && time.Until(t) > 0 {
				readDlT = time.NewTimer(time.Until(t))
				readDl = readDlT.C
			}
		case t := <-ch.setWriteDl:
			if writeDlT != nil {
				writeDlT.Stop()
				writeDlT = nil
				writeDl = nil
			}
			ch.writeDeadline = t
			if !t.IsZero() && time.Until(t) > 0 {
				writeDlT = time.NewTimer(time.Until(t))
				writeDl = writeDlT.C
			}
		case <-readDl:
			ch.inCond.Broadcast()
		case <-writeDl:
			ch.inCond.Broadcast()
		}
	}
}

func (ch *Channel) Read(b []byte) (n int, err error) {
	ch.inLock.Lock()
	defer ch.inLock.Unlock()

	for {
		if len(ch.bufIn) > 0 {
			n = copy(b, ch.bufIn)
			if len(ch.bufIn) <= n {
				// copied all buf to b, remove buf
				ch.bufIn = nil
			} else {
				// copy what we can to b
				ch.bufIn = ch.bufIn[n:]
			}
			return
		}
		if ch.closed != 0 {
			return 0, io.EOF
		}
		if len(b) == 0 {
			return
		}

		if !ch.readDeadline.IsZero() && time.Until(ch.readDeadline) < 0 {
			err = ErrTimeout
			return
		}

		ch.inCond.Wait()
	}
}

func (ch *Channel) Write(b []byte) (int, error) {
	ch.outLock.Lock()
	defer ch.outLock.Unlock()
	var n int
	var c <-chan time.Time

	if !ch.writeDeadline.IsZero() {
		t := time.NewTimer(time.Until(ch.writeDeadline))
		defer t.Stop()
		c = t.C
	}

	for {
		if ch.s.closed != 0 {
			return n, io.ErrClosedPipe
		}

		snd := uint32(len(b))
		if snd == 0 {
			return n, nil
		}
		if snd > maxFramePayload {
			snd = maxFramePayload
		}

		sB := make([]byte, snd)
		copy(sB, b)
		b = b[int(snd):]

		select {
		case ch.s.outData <- &frame{frameData, ch.ch, sB}:
			runtime.Gosched()
		case <-ch.s.cl:
			return n, io.ErrClosedPipe
		case <-ch.cl:
			return n, io.ErrClosedPipe
		case <-c:
			return n, ErrTimeout
		}
		n += int(snd)
	}
}

func (ch *Channel) waitAccept() error {
	ch.inLock.Lock()
	defer ch.inLock.Unlock()
	// wait for frameOpenAck
	for {
		if ch.accepted {
			return nil
		}
		if ch.err != nil {
			return ch.err
		}
		if ch.closed != 0 {
			return io.ErrClosedPipe
		}
		ch.inCond.Wait()
	}
}

func doResolveAddr(network, addr string) (net.Addr, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return net.ResolveTCPAddr(network, addr)
	case "udp", "udp4", "udp6":
		return net.ResolveUDPAddr(network, addr)
	case "unix":
		return net.ResolveUnixAddr(network, addr)
	default:
		return nil, net.UnknownNetworkError(network)
	}
}

func (ch *Channel) handle(f *frame) {
	switch f.code {
	case frameOpenAck:
		ch.accepted = true
		ch.inCond.Broadcast()
	case frameOpenError:
		ch.inLock.Lock()
		defer ch.inLock.Unlock()
		ch.err = errors.New(string(f.payload))
		ch.inCond.Broadcast()
	case frameData:
		if f.payload == nil {
			return
		}
		ch.inLock.Lock()
		defer ch.inLock.Unlock()
		ch.bufIn = append(ch.bufIn, f.payload...)
		ch.inCond.Broadcast() // broadcast in case multiple readers aren't reading much
	case frameSetName:
		// decode payload
		info := strings.Split(string(f.payload), "\x00")
		if info[0] != "" {
			// set laddr
			a, err := doResolveAddr(info[0], info[1])
			if err == nil {
				ch.laddr = a
			}
		}
		if info[2] != "" {
			// set raddr
			a, err := doResolveAddr(info[2], info[3])
			if err == nil {
				ch.raddr = a
			}
		}
	case frameClose:
		ch.Close()
	}
}

func (ch *Channel) Close() error {
	if atomic.AddUint32(&ch.closed, 1) != 1 {
		return nil
	}

	if ch.s.closed == 0 {
		select {
		case ch.s.out <- &frame{frameClose, ch.ch, nil}:
		case <-ch.s.cl:
			return io.ErrClosedPipe
		}
	}

	ch.s.unregCh(ch.ch)

	// this will cause io.EOF to be sent to all readers
	ch.inCond.Broadcast()

	// TODO close
	close(ch.cl)
	return nil
}

func (ch *Channel) Endpoint() (string, string) {
	ep := bytes.SplitN(ch.ep, []byte{0}, 2)

	switch len(ep) {
	case 0:
		return "", ""
	case 1:
		return string(ep[0]), ""
	default:
		return string(ep[0]), string(ep[1])
	}
}

func (ch *Channel) SetName(local, remote net.Addr) error {
	// send a frameSetName with local & remote info
	var data []string

	if remote != nil {
		data = append(data, remote.Network(), remote.String())
	} else {
		data = append(data, "", "")
	}
	if local != nil {
		data = append(data, local.Network(), local.String())
	} else {
		data = append(data, "", "")
	}

	select {
	case ch.s.out <- &frame{frameSetName, ch.ch, []byte(strings.Join(data, "\x00"))}:
	case <-ch.s.cl:
		return io.ErrClosedPipe
	}

	ch.laddr = local
	ch.raddr = remote

	return nil
}

func (ch *Channel) LocalAddr() net.Addr {
	return ch.laddr
}

func (ch *Channel) RemoteAddr() net.Addr {
	return ch.raddr
}

func (ch *Channel) SetDeadline(t time.Time) error {
	go ch.timeoutH.Do(ch.processTimeout)
	ch.setReadDl <- t
	ch.setWriteDl <- t
	return nil
}

func (ch *Channel) SetReadDeadline(t time.Time) error {
	go ch.timeoutH.Do(ch.processTimeout)
	ch.setReadDl <- t
	return nil
}

func (ch *Channel) SetWriteDeadline(t time.Time) error {
	go ch.timeoutH.Do(ch.processTimeout)
	ch.setWriteDl <- t
	return nil
}
