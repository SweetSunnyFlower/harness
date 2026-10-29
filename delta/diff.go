package delta

import "strings"

// DiffRevisions produces ops that turn before into after. The sequence is
// conservative and valid; it is not byte-identical to TypeScript, and only the
// resulting value is contractual.
func DiffRevisions(before, after any) []Op {
	var ops []Op
	diffAt(before, after, Path{}, &ops)
	return ops
}

func diffAt(before, after any, path Path, ops *[]Op) {
	if jsonEqual(before, after) {
		return
	}
	beforeObj, bok := before.(map[string]any)
	afterObj, aok := after.(map[string]any)
	if bok && aok {
		for _, k := range keyOrderOfUnion(beforeObj, afterObj) {
			bv, bin := beforeObj[k]
			av, ain := afterObj[k]
			switch {
			case bin && !ain:
				*ops = append(*ops, Delete{Path: appendPath(path, Key(k))})
			case !bin && ain:
				*ops = append(*ops, Set{Path: appendPath(path, Key(k)), Value: deepCopy(av)})
			default:
				diffAt(bv, av, appendPath(path, Key(k)), ops)
			}
		}
		return
	}
	beforeStr, bsok := before.(string)
	afterStr, asok := after.(string)
	if bsok && asok && strings.HasPrefix(afterStr, beforeStr) && len(afterStr) > len(beforeStr) {
		*ops = append(*ops, Append{Path: appendPath(path), Text: afterStr[len(beforeStr):]})
		return
	}
	if len(path) == 0 {
		*ops = append(*ops, Replace{Value: deepCopy(after)})
		return
	}
	*ops = append(*ops, Set{Path: path, Value: deepCopy(after)})
}

func appendPath(path Path, segs ...Seg) Path {
	out := make(Path, 0, len(path)+len(segs))
	out = append(out, path...)
	return append(out, segs...)
}

func keyOrderOfUnion(a, b map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keyOrder(a) {
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range keyOrder(b) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}
