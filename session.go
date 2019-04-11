package xmux

import (
	"io"
	"sync/atomic"
)

type Session struct {
	s   io.ReadWriter
	srv bool

	chId  uint32
	chMap map[uint32]*Channel

	chWinSize uint32
	closed    uint32
	cl        chan struct{} // close channel
}

func New(s io.ReadWriter, server bool) *Session {
	res := &Session{
		s:         s,
		srv:       server,
		chWinSize: 1048576, // defualt window of 1MB
		chMap:     make(map[uint32]*Channel),
		cl:        make(chan struct{}),
	}
	if server {
		res.chId = 2
	} else {
		res.chId = 1
	}

	return res
}

func (s *Session) Close() error {
	if atomic.AddUint32(&s.closed, 1) != 1 {
		// was already closed
		return nil
	}

	close(s.cl)
	// close channels, etc
	// TODO
	return nil
}
