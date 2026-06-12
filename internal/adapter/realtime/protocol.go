package realtime

import (
	"encoding/json"
	"strconv"
	"time"

	"deciscope-core-api/internal/domain"
)

type eventMessage struct {
	Type      string          `json:"type"`
	MeetingID string          `json:"meeting_id"`
	Seq       int64           `json:"seq,omitempty"`
	TsMS      int64           `json:"ts_ms"`
	Payload   json.RawMessage `json:"payload"`
}

type clientHello struct {
	Type      string `json:"type"`
	MeetingID string `json:"meeting_id"`
	LastSeq   int64  `json:"last_seq"`
}

func eventProtocolMessage(event domain.Event) eventMessage {
	return eventMessage{
		Type: event.Type, MeetingID: event.MeetingID, Seq: event.Seq,
		TsMS: event.TsMS, Payload: event.Payload,
	}
}

func catchUpErrorMessage() map[string]any {
	return map[string]any{
		"type": "error",
		"payload": map[string]any{
			"code":      "catchup_failed",
			"message":   "failed to load missed events",
			"retryable": true,
		},
	}
}

func readHello(conn netConn, reader frameReader, meetingID string) (int64, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	opcode, payload, err := readFrame(reader)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || opcode != opText {
		return 0, false
	}
	var hello clientHello
	if err := json.Unmarshal(payload, &hello); err != nil {
		return 0, false
	}
	if hello.Type != "client.hello" || hello.MeetingID != meetingID {
		return 0, false
	}
	return hello.LastSeq, true
}

func parseSeq(value string) int64 {
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
