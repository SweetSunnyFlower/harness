package storage

import (
	"encoding/json"
	"fmt"

	"harness/delta"
)

func (c DocumentContent) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case "base":
		return json.Marshal(map[string]any{"version": c.Version, "kind": "base", "value": c.Value})
	case "delta":
		return json.Marshal(map[string]any{"version": c.Version, "kind": "delta", "ops": c.Ops})
	default:
		return nil, fmt.Errorf("storage: unknown document content kind %q", c.Kind)
	}
}

func (c *DocumentContent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Version float64           `json:"version"`
		Kind    string            `json:"kind"`
		Value   json.RawMessage   `json:"value"`
		Ops     []json.RawMessage `json:"ops"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Version = raw.Version
	c.Kind = raw.Kind
	switch raw.Kind {
	case "base":
		if err := json.Unmarshal(raw.Value, &c.Value); err != nil {
			return err
		}
	case "delta":
		ops := make([]delta.Op, 0, len(raw.Ops))
		for _, item := range raw.Ops {
			op, err := delta.UnmarshalOp(item)
			if err != nil {
				return err
			}
			ops = append(ops, op)
		}
		c.Ops = ops
	default:
		return fmt.Errorf("storage: unknown document content kind %q", raw.Kind)
	}
	return nil
}
