package record

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrRecord = errors.New("invalid durable record")

// marshalWithExtra encodes typed fields and overlays preserved unknown members.
// Known fields win over extras on key collisions.
func marshalWithExtra(fields any, extra map[string]json.RawMessage) ([]byte, error) {
	known, err := objectOf(fields)
	if err != nil {
		return nil, err
	}
	for k, v := range extra {
		if _, exists := known[k]; !exists {
			known[k] = v
		}
	}
	return json.Marshal(known)
}

// unmarshalWithExtra decodes typed fields and keeps every member the typed
// fields do not claim as an unknown extra, preserving unknown content.
func unmarshalWithExtra(data []byte, fields any, extra *map[string]json.RawMessage) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	if err := json.Unmarshal(data, fields); err != nil {
		return fmt.Errorf("%w: %w", ErrRecord, err)
	}
	known, err := objectOf(fields)
	if err != nil {
		return err
	}
	out := make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		if _, claimed := known[k]; !claimed {
			out[k] = v
		}
	}
	*extra = out
	return nil
}

func objectOf(v any) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRecord, fmt.Sprintf(format, args...))
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0
}
