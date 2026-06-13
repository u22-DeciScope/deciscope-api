package realtime

import (
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

type client struct {
	meetingID string
	conn      netConn
	reader    frameReader
	send      chan domain.Event
	done      chan struct{}
	writeMu   sync.Mutex
	lastSeq   int64
}

func newClient(meetingID string, conn netConn, reader frameReader, lastSeq int64) *client {
	return &client{
		meetingID: meetingID,
		conn:      conn,
		reader:    reader,
		send:      make(chan domain.Event, 128),
		done:      make(chan struct{}),
		lastSeq:   lastSeq,
	}
}

func (c *client) enqueue(event domain.Event) {
	select {
	case c.send <- event:
	default:
		select {
		case <-c.send:
		default:
		}
		c.send <- event
	}
}

func (c *client) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event := <-c.send:
			if err := c.writeEvent(event); err != nil {
				return
			}
		case <-ticker.C:
			c.writeMu.Lock()
			err := writePing(c.conn)
			c.writeMu.Unlock()
			if err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *client) readLoop() {
	for {
		opcode, payload, err := readFrame(c.reader)
		if err != nil {
			return
		}
		switch opcode {
		case opClose:
			return
		case opPing:
			c.writeMu.Lock()
			_ = writePong(c.conn, payload)
			c.writeMu.Unlock()
		}
	}
}

func (c *client) writeEvent(event domain.Event) error {
	if event.Seq > 0 && event.Seq <= c.lastSeq {
		return nil
	}
	if event.Seq > c.lastSeq {
		c.lastSeq = event.Seq
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeJSON(c.conn, eventProtocolMessage(event))
}
