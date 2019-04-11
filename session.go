package xmux

import "io"

type Session struct {
	s   io.ReadWriter
	srv bool

	chId uint32

	chWinSize uint32
}

func New(s io.ReadWriter, server bool) *Session {
	res := &Session{
		s:         s,
		srv:       server,
		chWinSize: 1048576, // defualt window of 1MB
	}
	if server {
		res.chId = 2
	} else {
		res.chId = 1
	}

	return res
}
