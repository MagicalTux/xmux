package xmux

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Session struct {
	s   io.ReadWriter
	srv bool

	chId  uint32
	chMap map[uint32]*Channel
	chMlk sync.RWMutex

	accept chan *Channel

	t         time.Time // expiration timer
	chWinSize uint32
	closed    uint32
	out       chan *frame
	cl        chan struct{} // close channel
	err       error
}

func New(s io.ReadWriter, server bool) *Session {
	res := &Session{
		s:         s,
		t:         time.Now().Add(60 * time.Second),
		srv:       server,
		accept:    make(chan *Channel, 100),
		chWinSize: 1048576, // defualt window of 1MB
		chMap:     make(map[uint32]*Channel),
		out:       make(chan *frame, 100),
		cl:        make(chan struct{}),
	}
	if server {
		res.chId = 2
	} else {
		res.chId = 1
	}

	go res.writeRoutine()
	go res.readRoutine()

	return res
}

func (s *Session) AcceptChannel() (*Channel, error) {
	c, ok := <-s.accept
	if !ok {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.ErrClosedPipe
	}
	s.out <- &frame{frameOpenAck, c.ch, nil}
	c.winCalc()
	return c, nil
}

func (s *Session) Accept() (net.Conn, error) {
	return s.AcceptChannel()
}

func (s *Session) Dial(network, address string) (net.Conn, error) {
	return s.DialChannel(network, address)
}

func (s *Session) DialChannel(network, address string) (*Channel, error) {
	ep := []byte(network + "\x00" + address)

	cid := atomic.AddUint32(&s.chId, 2) - 2
	ch := s.newChannel(cid, ep)

	s.chMlk.Lock()
	s.chMap[cid] = ch
	s.chMlk.Unlock()

	s.out <- &frame{frameOpenChannel, cid, ep}

	err := ch.waitAccept()
	if err != nil {
		s.unregCh(cid)
		return nil, err
	}

	// TODO wait for channel open response
	return ch, nil
}

func (s *Session) unregCh(ch uint32) {
	s.chMlk.Lock()
	defer s.chMlk.Unlock()
	delete(s.chMap, ch)
}

// Addr complies with interface net.Listener and returns local addr if any
func (s *Session) Addr() net.Addr {
	if o, ok := s.s.(interface{ LocalAddr() net.Addr }); ok {
		return o.LocalAddr()
	}
	return nil // TODO return something else than nil?
}

func (s *Session) Close() error {
	if atomic.AddUint32(&s.closed, 1) != 1 {
		// was already closed
		return nil
	}

	close(s.cl)

	// close channels, etc
	s.chMlk.Lock()
	for _, ch := range s.chMap {
		// do close in separate routine because it'll need the lock we hold
		go ch.Close()
	}
	s.chMlk.Unlock()

	// TODO wait for group?

	if cl, ok := s.s.(io.Closer); ok {
		cl.Close()
	}

	return nil
}

func (s *Session) readRoutine() {
	buf := bufio.NewReader(s.s)
	for {
		f, err := readFrame(buf)
		if err != nil {
			log.Printf("xmux: failed to read from peer: %s", err)
			if s.err != nil {
				s.err = err
			}
			s.Close()
			return
		}

		// route/handle frame (TODO)
		switch f.code {
		case frameOpenChannel:
			if f.ch == 0 {
				// channel 0 → not allowed to open
				break
			}
			if s.srv {
				if f.ch&1 == 0 {
					log.Printf("xmux: received server chid open while we are server")
				}
			} else {
				if f.ch&1 == 1 {
					log.Printf("xmux: received client chid open while we are client")
				}
			}
			ch := s.newChannel(f.ch, f.payload)
			s.chMlk.Lock()
			if _, ok := s.chMap[f.ch]; ok {
				s.chMlk.Unlock()
				// shouldn't happen
				log.Printf("xmux: request for open existing channel, ignored")
				break
			}
			s.chMap[f.ch] = ch
			s.chMlk.Unlock()

			s.accept <- ch
		case frameOpenAck, frameWinAdjust, frameData, frameClose:
			if f.ch == 0 {
				break // nope
			}
			s.chMlk.RLock()
			ch, ok := s.chMap[f.ch]
			s.chMlk.RUnlock()
			if !ok {
				break
			}
			ch.handle(f)
		case ctrlKeepAlive:
			s.t = time.Now().Add(60 * time.Second)
		case ctrlError:
			if f.payload != nil {
				s.err = errors.New(string(f.payload))
			}
			s.Close()
		}
	}
}

func (s *Session) writeRoutine() {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()

	for {
		select {
		case f, ok := <-s.out:
			if !ok {
				// likely a close signal
				return
			}
			f.WriteTo(s.s)
		case <-s.cl:
			// close signal
			return
		case <-tk.C:
			if time.Until(s.t) < 0 {
				s.err = errors.New("xmux: connection timed out")
				s.Close()
				return
			}
			// send keepalive
			f := &frame{ctrlKeepAlive, 0, nil}
			f.WriteTo(s.s)
		}
	}
}
