package core

import (
	"encoding/json"
)

func jsonPayload(payload any) (json.RawMessage, error) {
	switch p := payload.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return p, nil
	case []byte:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return json.RawMessage(p), nil
	default:
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}
