package xmux

var ErrTimeout error = errTimeout("xmux: timed out")

type errTimeout string

func (e errTimeout) Error() string {
	return string(e)
}

// implement interface net.Error

func (e errTimeout) Timeout() bool {
	return true
}

func (e errTimeout) Temporary() bool {
	return true // really?
}
