package xmux

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

const (
	frameOpenChannel = iota // open channel = connect()
	frameOpenAck            // channel open success
	frameWinAdjust          // adjust in window for channel
	frameData               // sending data to channel
	frameClose              // close channel

	ctrlKeepAlive
	ctrlError
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
	var c uint8
	err := binary.Read(r, binary.BigEndian, &c)
	if err != nil {
		return nil, err
	}

	var ch uint32
	err = binary.Read(r, binary.BigEndian, &ch)
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

// WriteTo conforms to the right go structure
func (f *frame) WriteTo(w io.Writer) (int64, error) {
	hdr := make([]byte, 5+binary.MaxVarintLen64)
	hdr[0] = byte(f.code)
	binary.BigEndian.PutUint32(hdr[1:5], f.ch)
	n := binary.PutUvarint(hdr[5:], uint64(len(f.payload)))

	// write
	n2, err := w.Write(hdr[:5+n])
	if err != nil {
		return int64(n2), err
	}

	n3, err := w.Write(f.payload)
	return int64(n2 + n3), err
}
