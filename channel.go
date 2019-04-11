package xmux

import (
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
)

type Channel struct {
	s             *Session
	ch            uint32
	ep            []byte
	winIn, winOut uint32
	cl            chan struct{} // close chan
	inLock        sync.Mutex
	inCond        *sync.Cond
	closed        uint32

	bufIn []byte
}

func (s *Session) newChannel(ch uint32, ep []byte) *Channel {
	res := &Channel{
		s:  s,
		ch: ch,
		ep: ep,
		cl: make(chan struct{}),
	}

	res.inCond = sync.NewCond(&res.inLock)

	return res
}

func (ch *Channel) Read(b []byte) (n int, err error) {
	ch.inLock.Lock()
	defer ch.inLock.Unlock()

	for {
		if ch.closed != 0 {
			return 0, io.EOF
		}
		if len(b) == 0 {
			return
		}
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
		ch.winOut = binary.BigEndian.Uint32(f.payload)
	case frameData:
		if f.payload == nil {
			return
		}
		ch.inLock.Lock()
		defer ch.inLock.Unlock()
		ch.bufIn = append(ch.bufIn, f.payload...)
		ch.winIn -= uint32(len(f.payload)) // TODO check overflow?
		ch.inCond.Signal()
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

	if ch.s.closed == 0 {
		ch.s.out <- &frame{frameWinAdjust, ch.ch, payload}
	}
}
