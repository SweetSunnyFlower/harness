package mysql_test

import (
	"os"
	"testing"

	"harness/storage"
	"harness/storage/conformance"
	mysqlstorage "harness/storage/mysql"
)

func TestMySQLConformance(t *testing.T) {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		t.Skip("MYSQL_DSN not set; set it to a disposable database DSN to run")
	}
	conformance.Conformance(t, func(t *testing.T) storage.Storage {
		s, err := mysqlstorage.Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close(nil) })
		return s
	})
}
