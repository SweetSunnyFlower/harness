package storage_test

import (
	"testing"

	"harness/storage"
	"harness/storage/conformance"
)

func TestMemoryConformance(t *testing.T) {
	conformance.Conformance(t, func(t *testing.T) storage.Storage {
		return storage.NewMemory()
	})
}
