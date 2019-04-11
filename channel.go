package xmux

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Channel struct {
	s             *Session
	ch            uint32
	ep            []byte
	winIn, winOut uint32
	cl            chan struct{} // close chan
	accepted      bool

	bufIn   []byte
	inLock  sync.Mutex
	inCond  *sync.Cond
	outLock sync.Mutex
	outCond *sync.Cond

	laddr, raddr net.Addr

	closed uint32
}

func (s *Session) newChannel(ch uint32, ep []byte) *Channel {
	res := &Channel{
		s:  s,
		ch: ch,
		ep: ep,
		cl: make(chan struct{}),
	}

	res.inCond = sync.NewCond(&res.inLock)
	res.outCond = sync.NewCond(&res.outLock)

	return res
}

func (ch *Channel) Read(b []byte) (n int, err error) {
	ch.inLock.Lock()
	defer ch.inLock.Unlock()

	for {
		if len(ch.bufIn) > 0 {
			copy(b, ch.bufIn)
			if len(ch.bufIn) <= len(b) {
				// copied all buf to b, remove buf
				n = len(ch.bufIn)
				ch.bufIn = nil
			} else {
				// copy what we can to b
				n = len(b)
				b = b[n:]
			}
			ch.winCalcLk()
			return
		}
		if ch.closed != 0 {
			return 0, io.EOF
		}
		if len(b) == 0 {
			return
		}

		ch.inCond.Wait()
	}
}

func (ch *Channel) Write(b []byte) (int, error) {
	ch.outLock.Lock()
	defer ch.outLock.Unlock()
	var n int

	for {
		if ch.s.closed != 0 {
			return n, io.EOF
		}

		snd := uint32(len(b))
		if snd > ch.winOut {
			snd = ch.winOut
		}
		if snd > maxFramePayload {
			snd = maxFramePayload
		}

		ch.winOut -= snd

		// TODO copy buffer?
		if uint32(len(b)) <= snd {
			ch.s.out <- &frame{frameData, ch.ch, b}
			n += int(snd)
			return n, nil
		} else {
			sB := b[:int(snd)]
			b = b[int(snd):]
			ch.s.out <- &frame{frameData, ch.ch, sB}
			n += int(snd)
		}

		if ch.winOut == 0 {
			ch.outCond.Wait()
		}
	}
}

func (ch *Channel) waitAccept() error {
	ch.inLock.Lock()
	defer ch.inLock.Unlock()
	// wait for frameOpenAck
	for {
		if ch.closed != 0 {
			return io.ErrClosedPipe
		}
		if ch.accepted {
			return nil
		}
		ch.inCond.Wait()
	}
}

func (ch *Channel) handle(f *frame) {
	switch f.code {
	case frameOpenAck:
		ch.winCalc()
	case frameWinAdjust:
		if len(f.payload) != 4 {
			return
		}
		ch.outLock.Lock()
		defer ch.outLock.Unlock()
		ch.winOut = binary.BigEndian.Uint32(f.payload)
		ch.outCond.Broadcast()
	case frameData:
		if f.payload == nil {
			return
		}
		ch.inLock.Lock()
		defer ch.inLock.Unlock()
		ch.bufIn = append(ch.bufIn, f.payload...)
		ch.winIn -= uint32(len(f.payload)) // TODO check overflow?
		ch.inCond.Broadcast()              // broadcast in case multiple readers aren't reading much
	case frameClose:
		ch.Close()
	}
}

func (ch *Channel) Close() error {
	if atomic.AddUint32(&ch.closed, 1) != 1 {
		return nil
	}

	if ch.s.closed == 0 {
		ch.s.out <- &frame{frameClose, ch.ch, nil}
	}

	ch.s.unregCh(ch.ch)

	// this will cause io.EOF to be sent to all readers
	ch.inCond.Broadcast()

	// TODO close
	close(ch.cl)
	return nil
}

func (ch *Channel) winCalc() {
	ch.inLock.Lock()
	defer ch.inLock.Unlock()

	ch.winCalcLk()
}

func (ch *Channel) winCalcLk() {
	// we have the lock here
	maxWin := ch.s.chWinSize

	if (uint32(len(ch.bufIn)) + ch.winIn) >= maxWin {
		return
	}

	// increase winIn
	ch.winIn = maxWin - uint32(len(ch.bufIn))
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, ch.winIn)
	ch.accepted = true

	if ch.s.closed == 0 {
		ch.s.out <- &frame{frameWinAdjust, ch.ch, payload}
	}
	ch.inCond.Broadcast()
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

func (ch *Channel) LocalAddr() net.Addr {
	return ch.laddr
}

func (ch *Channel) RemoteAddr() net.Addr {
	return ch.raddr
}

func (ch *Channel) SetDeadline(t time.Time) error {
	return errors.New("TODO")
}

func (ch *Channel) SetReadDeadline(t time.Time) error {
	return errors.New("TODO")
}

func (ch *Channel) SetWriteDeadline(t time.Time) error {
	return errors.New("TODO")
}
