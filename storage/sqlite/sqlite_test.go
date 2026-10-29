package sqlite_test

import (
	"path/filepath"
	"testing"

	"harness/storage"
	"harness/storage/conformance"
	sqlitestorage "harness/storage/sqlite"
)

func TestSQLiteConformance(t *testing.T) {
	conformance.Conformance(t, func(t *testing.T) storage.Storage {
		path := filepath.Join(t.TempDir(), "storage.sqlite")
		s, err := sqlitestorage.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close(nil) })
		return s
	})
}
