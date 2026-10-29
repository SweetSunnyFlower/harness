package delta

func ApplyImmutable(root any, ops []Op) (any, error) {
	return applyOps(root, ops, newOwnedSet())
}

func ApplyImmutableBatches(root any, batches [][]Op) (any, error) {
	owned := newOwnedSet()
	for _, ops := range batches {
		next, err := applyOps(root, ops, owned)
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}
