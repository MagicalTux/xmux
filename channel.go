package xmux

type Channel struct {
	s             *Session
	ch            uint32
	winIn, winOut uint32
}
