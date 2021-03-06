package xmux

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	frameOpenChannel = iota // open channel = connect()
	frameOpenAck            // channel open success
	frameOpenError          // error happened during open
	frameData               // sending data to channel
	frameClose              // close channel
	frameSetName            // set name (localaddr, remoteaddr) of a channel

	ctrlKeepAlive
	ctrlError
)

const (
	maxFramePayload = 65536
)

type frame struct {
	code    uint8  // code
	ch      uint32 // channel
	payload []byte // data
}

func frameCodeName(code uint8) string {
	switch code {
	case frameOpenChannel:
		return "open"
	case frameOpenAck:
		return "ack"
	case frameOpenError:
		return "refused"
	case frameData:
		return "data"
	case frameClose:
		return "close"
	case frameSetName:
		return "name"
	case ctrlKeepAlive:
		return "ctrlKeep"
	case ctrlError:
		return "ctrlError"
	default:
		return fmt.Sprintf("unknownCode(%d)", code)
	}
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

	if l > 0 {
		pl := make([]byte, int(l))

		_, err = io.ReadFull(r, pl)
		if err != nil {
			return nil, err
		}
		return &frame{c, ch, pl}, nil
	}

	return &frame{c, ch, nil}, nil
}

// WriteTo conforms to the appropriate go interface
func (f *frame) WriteTo(w io.Writer) (int64, error) {
	hdr := make([]byte, 5+binary.MaxVarintLen64)
	hdr[0] = byte(f.code)
	binary.BigEndian.PutUint32(hdr[1:5], f.ch)
	n := binary.PutUvarint(hdr[5:], uint64(len(f.payload)))

	n2, err := w.Write(hdr[:5+n])

	if len(f.payload) == 0 {
		return int64(n2), err
	}

	// write
	n3, err := w.Write(f.payload)
	return int64(n2 + n3), err
}
