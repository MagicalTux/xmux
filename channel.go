package xmux

import (
	"encoding/binary"
	"sync"
)

type Channel struct {
	s             *Session
	ch            uint32
	ep            []byte
	winIn, winOut uint32
	cl            chan struct{} // close chan
	winLock       sync.Mutex

	bufIn []byte
}

func (s *Session) newChannel(ch uint32, ep []byte) *Channel {
	return &Channel{
		s:  s,
		ch: ch,
		ep: ep,
		cl: make(chan struct{}),
	}
}

func (ch *Channel) handle(f *frame) {
	switch f.code {
	case frameOpenAck:
		ch.winCalc()
	}
}

func (ch *Channel) winCalc() {
	ch.winLock.Lock()
	defer ch.winLock.Unlock()

	maxWin := ch.s.chWinSize

	if (uint32(len(ch.bufIn)) + ch.winIn) >= maxWin {
		return
	}

	diff := maxWin - (uint32(len(ch.bufIn)) + ch.winIn)
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, diff)

	ch.s.out <- &frame{frameWinAdjust, ch.ch, payload}
}
