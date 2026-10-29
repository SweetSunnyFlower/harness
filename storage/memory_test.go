package storage_test

import (
	"testing"

	"harness/storage"
)

func TestMemoryConformance(t *testing.T) {
	Conformance(t, func(t *testing.T) storage.Storage {
		return storage.NewMemory()
	})
}
