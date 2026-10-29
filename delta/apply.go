package delta

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"unicode/utf16"
	"unicode/utf8"
)

func Apply(root any, ops []Op) (any, error) {
	return applyOps(root, ops, nil)
}

func applyOps(root any, ops []Op, owned *ownedSet) (any, error) {
	for _, op := range ops {
		if op == nil {
			return nil, fmt.Errorf("%w: op is nil", ErrInvalidOp)
		}
		if err := op.Validate(); err != nil {
			return nil, err
		}
		next, err := applyOne(root, op, owned)
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func applyOne(root any, op Op, owned *ownedSet) (any, error) {
	switch op := op.(type) {
	case Replace:
		return op.Value, nil
	case Splice:
		return descend(root, op.Path, owned, func(node any) (any, error) {
			xs, ok := node.([]any)
			if !ok {
				return nil, &PathError{Ref: op.Path}
			}
			if owned != nil {
				if appended, ok := appendCopy(xs, op, owned); ok {
					return appended, nil
				}
				xs = owned.claim(xs).([]any)
			}
			return splice(xs, op.Index, op.Remove, op.Items), nil
		})
	case Permute:
		return walk(root, op.Path, owned, func(node any) (any, error) {
			xs, ok := node.([]any)
			if !ok || len(xs) != len(op.Permutation) {
				return nil, &PathError{Ref: op.Path}
			}
			previous := slices.Clone(xs)
			for i, from := range op.Permutation {
				xs[i] = previous[from]
			}
			return xs, nil
		})
	case Set:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return write(parent, key, op.Path, func(any) (any, error) { return op.Value, nil })
		})
	case Delete:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return remove(parent, key, op.Path)
		})
	case Append:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return write(parent, key, op.Path, func(current any) (any, error) {
				s, ok := current.(string)
				if !ok {
					return nil, &PathError{Ref: op.Path}
				}
				return s + op.Text, nil
			})
		})
	case Truncate:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return write(parent, key, op.Path, func(current any) (any, error) {
				s, ok := current.(string)
				if !ok {
					return nil, &PathError{Ref: op.Path}
				}
				return truncateUTF16(s, op.Count), nil
			})
		})
	}
	return nil, fmt.Errorf("%w: unknown op %T", ErrInvalidOp, op)
}

func walk(root any, path Path, owned *ownedSet, fn func(node any) (any, error)) (any, error) {
	return descend(root, path, owned, func(node any) (any, error) {
		if owned != nil {
			node = owned.claim(node)
		}
		return fn(node)
	})
}

func descend(root any, path Path, owned *ownedSet, fn func(node any) (any, error)) (any, error) {
	var step func(node any, rest Path) (any, error)
	step = func(node any, rest Path) (any, error) {
		if len(rest) == 0 {
			next, err := fn(node)
			if err == nil && owned != nil {
				owned.adopt(next)
			}
			return next, err
		}
		if owned != nil {
			node = owned.claim(node)
		}
		seg := rest[0]
		switch c := node.(type) {
		case map[string]any:
			key := propertyKey(seg)
			child, ok := c[key]
			if !ok {
				return nil, &PathError{Ref: path}
			}
			next, err := step(child, rest[1:])
			if err != nil {
				return nil, err
			}
			c[key] = next
			return c, nil
		case []any:
			i, ok := seg.(Index)
			if !ok {
				if owned != nil && !ownsKey(c, seg) {
					return nil, &PathError{Ref: path}
				}
				return nil, &UnsafePathError{Segment: seg}
			}
			if i >= Index(len(c)) {
				return nil, &PathError{Ref: path}
			}
			next, err := step(c[i], rest[1:])
			if err != nil {
				return nil, err
			}
			c[i] = next
			return c, nil
		}
		return nil, &PathError{Ref: path}
	}
	return step(root, path)
}

func atParent(root any, path Path, owned *ownedSet, fn func(parent any, key Seg) (any, error)) (any, error) {
	parentPath := path[:len(path)-1]
	return walk(root, parentPath, owned, func(node any) (any, error) {
		switch node.(type) {
		case map[string]any, []any:
		default:
			return nil, &PathError{Ref: parentPath}
		}
		return fn(node, path[len(path)-1])
	})
}

func write(parent any, key Seg, path Path, fn func(current any) (any, error)) (any, error) {
	switch p := parent.(type) {
	case map[string]any:
		k := propertyKey(key)
		v, err := fn(p[k])
		if err != nil {
			return nil, err
		}
		p[k] = v
		return p, nil
	case []any:
		i, err := arrayIndex(p, key)
		if err != nil {
			return nil, err
		}
		if i == len(p) {
			v, err := fn(nil)
			if err != nil {
				return nil, err
			}
			return append(p, v), nil
		}
		v, err := fn(p[i])
		if err != nil {
			return nil, err
		}
		p[i] = v
		return p, nil
	}
	return nil, &PathError{Ref: path[:len(path)-1]}
}

func remove(parent any, key Seg, path Path) (any, error) {
	switch p := parent.(type) {
	case map[string]any:
		delete(p, propertyKey(key))
		return p, nil
	case []any:
		i, err := arrayIndex(p, key)
		if err != nil {
			return nil, err
		}
		if i >= len(p) {
			return nil, &PathError{Ref: path}
		}
		return slices.Delete(p, i, i+1), nil
	}
	return nil, &PathError{Ref: path[:len(path)-1]}
}

func propertyKey(seg Seg) string {
	switch s := seg.(type) {
	case Key:
		return string(s)
	case Index:
		return s.String()
	}
	return fmt.Sprint(seg)
}

func arrayIndex(xs []any, key Seg) (int, error) {
	i, ok := key.(Index)
	if !ok {
		return 0, &UnsafePathError{Segment: key}
	}
	if i > Index(len(xs)) {
		return 0, &UnsafePathError{Segment: key}
	}
	return int(i), nil
}

func splice(xs []any, index, remove int, items []any) []any {
	index = min(index, len(xs))
	remove = min(remove, len(xs)-index)
	xs = slices.Delete(xs, index, index+remove)
	return slices.Insert(xs, index, items...)
}

func truncateUTF16(s string, count int) string {
	i := 0
	for count > 0 && i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if utf16.RuneLen(r) == 2 && count == 1 {
			return "\uFFFD" + s[i+size:]
		}
		count -= utf16.RuneLen(r)
		i += size
	}
	return s[i:]
}

func appendCopy(xs []any, op Splice, owned *ownedSet) ([]any, bool) {
	if op.Remove != 0 || op.Index != len(xs) || owned.owns(xs) {
		return nil, false
	}
	appended := make([]any, 0, max(len(xs)+len(op.Items), 1))
	return append(append(appended, xs...), op.Items...), true
}

func ownsKey(xs []any, key Seg) bool {
	k := propertyKey(key)
	if k == "length" {
		return true
	}
	i, ok := canonicalIndex(k)
	return ok && i < int64(len(xs))
}

type ownedSet struct {
	at   map[uintptr]bool
	keep []any
}

func newOwnedSet() *ownedSet { return &ownedSet{at: map[uintptr]bool{}} }

func (o *ownedSet) owns(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return o.at[reflect.ValueOf(v).Pointer()]
	}
	return false
}

func (o *ownedSet) adopt(v any) {
	var ptr uintptr
	switch x := v.(type) {
	case map[string]any:
		if x == nil {
			return
		}
		ptr = reflect.ValueOf(v).Pointer()
	case []any:
		if cap(x) == 0 {
			return
		}
		ptr = reflect.ValueOf(v).Pointer()
	default:
		return
	}
	if !o.at[ptr] {
		o.at[ptr] = true
		o.keep = append(o.keep, v)
	}
}

func (o *ownedSet) claim(v any) any {
	if o.owns(v) {
		return v
	}
	var c any
	switch x := v.(type) {
	case map[string]any:
		if x == nil {
			return v
		}
		c = maps.Clone(x)
	case []any:
		c = make([]any, len(x), max(len(x), 1))
		copy(c.([]any), x)
	default:
		return v
	}
	o.adopt(c)
	return c
}
