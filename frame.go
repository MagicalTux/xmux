package xmux

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

const (
	frameOpenChannel = iota // open channel = connect()
	frameWinAdjust          // adjust in window for channel
	frameData               // sending data to channel
	frameClose              // close channel

	ctrlKeepAlive
)

const (
	maxFramePayload = 204800
)

type frame struct {
	ch      uint32 // channel
	code    uint8  // code
	payload []byte // data
}

func readFrame(r *bufio.Reader) (*frame, error) {
	var ch uint32
	err := binary.Read(r, binary.BigEndian, &ch)
	if err != nil {
		return nil, err
	}

	var c uint8
	err = binary.Read(r, binary.BigEndian, &c)
	if err != nil {
		return nil, err
	}

	l, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if l > maxFramePayload {
		return nil, errors.New("xmux: frame too large")
	}

	pl := make([]byte, int(l))

	_, err = io.ReadFull(r, pl)
	if err != nil {
		return nil, err
	}

	return &frame{ch, c, pl}, nil
}
