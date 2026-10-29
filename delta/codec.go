package delta

import "fmt"

type Encoder struct {
	seen map[string]bool
	ids  map[string]PathID
	next PathID
}

func (e *Encoder) Encode(ops []Op) []WireOp {
	previous := ""
	out := make([]WireOp, 0, len(ops))
	for _, op := range ops {
		if r, ok := op.(Replace); ok {
			out = append(out, r)
			clear(e.seen)
			clear(e.ids)
			e.next = 0
			previous = ""
			continue
		}
		path := opPath(op)
		key := path.String()
		if key == previous {
			out = append(out, toWire(op, nil))
			continue
		}
		var ref PathRef = path
		if id, ok := e.ids[key]; ok {
			ref = id
		} else if e.seen[key] {
			id := e.next
			e.next++
			if e.ids == nil {
				e.ids = map[string]PathID{}
			}
			e.ids[key] = id
			out = append(out, Define{ID: id, Path: path})
			ref = id
		} else {
			if e.seen == nil {
				e.seen = map[string]bool{}
			}
			e.seen[key] = true
		}
		out = append(out, toWire(op, ref))
		previous = key
	}
	return out
}

func opPath(op Op) Path {
	switch op := op.(type) {
	case Set:
		return op.Path
	case Delete:
		return op.Path
	case Append:
		return op.Path
	case Truncate:
		return op.Path
	case Splice:
		return op.Path
	case Permute:
		return op.Path
	}
	panic("delta: cannot encode replace or nil op")
}

func toWire(op Op, ref PathRef) WireOp {
	switch op := op.(type) {
	case Set:
		return WireSet{Ref: ref, Value: op.Value}
	case Delete:
		return WireDelete{Ref: ref}
	case Append:
		return WireAppend{Ref: ref, Text: op.Text}
	case Truncate:
		return WireTruncate{Ref: ref, Count: op.Count}
	case Splice:
		return WireSplice{Ref: ref, Index: op.Index, Remove: op.Remove, Items: op.Items}
	case Permute:
		return WirePermute{Ref: ref, Permutation: op.Permutation}
	}
	panic("delta: cannot encode replace or nil op")
}

type Decoder struct {
	paths map[PathID]Path
}

func (d *Decoder) Decode(wire []WireOp) ([]Op, error) {
	var previous Path
	hasPrevious := false
	out := make([]Op, 0, len(wire))
	for i, op := range wire {
		if op == nil {
			return nil, fmt.Errorf("delta: wire[%d]: %w: op is nil", i, ErrInvalidOp)
		}
		if err := op.Validate(); err != nil {
			return nil, fmt.Errorf("delta: wire[%d]: %w", i, err)
		}
		switch op := op.(type) {
		case Define:
			if d.paths == nil {
				d.paths = map[PathID]Path{}
			}
			d.paths[op.ID] = op.Path
			continue
		case Replace:
			out = append(out, op)
			clear(d.paths)
			hasPrevious = false
			continue
		}

		var path Path
		switch ref := wireRef(op).(type) {
		case nil:
			if !hasPrevious {
				return nil, fmt.Errorf("delta: wire[%d]: %w: short form reuses the previous op's path, and nothing before it in this batch has one", i, &PathError{Ref: Path{}})
			}
			path = previous
		case PathID:
			resolved, ok := d.paths[ref]
			if !ok {
				return nil, fmt.Errorf("delta: wire[%d]: %w: path id %d is not defined since the stream's last base batch", i, &PathError{Ref: ref}, ref)
			}
			path = resolved
			previous, hasPrevious = path, true
		case Path:
			path = ref
			previous, hasPrevious = path, true
		}

		decoded := fromWire(op, path)
		if !rootable(decoded) && len(path) == 0 {
			return nil, fmt.Errorf("delta: wire[%d]: %w: only \"r\" replaces the root, and only \"p\" and \"m\" may splice or permute a root array", i, &PathError{Ref: path})
		}
		out = append(out, decoded)
	}
	return out, nil
}

func wireRef(op WireOp) PathRef {
	switch op := op.(type) {
	case WireSet:
		return op.Ref
	case WireDelete:
		return op.Ref
	case WireAppend:
		return op.Ref
	case WireTruncate:
		return op.Ref
	case WireSplice:
		return op.Ref
	case WirePermute:
		return op.Ref
	}
	panic("delta: cannot decode replace or define")
}

func fromWire(op WireOp, path Path) Op {
	switch op := op.(type) {
	case WireSet:
		return Set{Path: path, Value: op.Value}
	case WireDelete:
		return Delete{Path: path}
	case WireAppend:
		return Append{Path: path, Text: op.Text}
	case WireTruncate:
		return Truncate{Path: path, Count: op.Count}
	case WireSplice:
		return Splice{Path: path, Index: op.Index, Remove: op.Remove, Items: op.Items}
	case WirePermute:
		return Permute{Path: path, Permutation: op.Permutation}
	}
	panic("delta: cannot decode replace or define")
}

func rootable(op Op) bool {
	switch op.(type) {
	case Splice, Permute:
		return true
	}
	return false
}
