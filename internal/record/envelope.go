package record

import (
	"encoding/json"
	"errors"
	"fmt"

	"harness/jsonvalue"
	"harness/protocol"
)

var ErrRecord = errors.New("record must be an object with a valid ID")

// Envelope preserves unknown data. It does not validate task or entry semantics.
type Envelope struct {
	id    protocol.RecordID
	value jsonvalue.Value
}

func Parse(data []byte) (Envelope, error) {
	value, err := jsonvalue.Parse(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrRecord, err)
	}
	if value.Kind() != jsonvalue.KindObject {
		return Envelope{}, ErrRecord
	}
	rawID, found, err := value.FieldASCII("id")
	if err != nil || !found {
		return Envelope{}, ErrRecord
	}
	var id protocol.RecordID
	if err := json.Unmarshal(rawID.Bytes(), &id); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrRecord, err)
	}
	return Envelope{id: id, value: value}, nil
}

func (e Envelope) ID() protocol.RecordID {
	return e.id
}

func (e Envelope) Value() jsonvalue.Value {
	return e.value
}

func (e Envelope) FieldASCII(name string) (jsonvalue.Value, bool, error) {
	return e.value.FieldASCII(name)
}

func (e Envelope) MarshalJSON() ([]byte, error) {
	if !e.id.Valid() {
		return nil, ErrRecord
	}
	return e.value.MarshalJSON()
}

func (e *Envelope) UnmarshalJSON(data []byte) error {
	next, err := Parse(data)
	if err != nil {
		return err
	}
	*e = next
	return nil
}
