package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestV1ToV2Migration(t *testing.T) {
	db := openRaw(t)
	if err := applyMigrations(db, 1); err != nil {
		t.Fatal(err)
	}
	waiting := `{"id":5,"conversationId":1,"kind":"app.task","version":1,"input":null,"background":false,"abortRequested":false,"state":{"status":"waiting","checkpoint":{"phase":"run"},"on":[],"policy":"allSettled"}}`
	if _, err := db.Exec(`INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record) VALUES (5, 1, 'app.task', 'waiting', 0, 0, ?)`, waiting); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record) VALUES (6, 1, 'app.task', 'sleeping', 0, 0, '{}')`); err == nil {
		t.Fatal("v1 schema must reject sleeping status")
	}
	if err := applyMigrations(db, 2); err != nil {
		t.Fatal(err)
	}
	var status, record string
	if err := db.QueryRow(`SELECT status, record FROM tasks WHERE id = 5`).Scan(&status, &record); err != nil {
		t.Fatal(err)
	}
	if status != "waiting" || record != waiting {
		t.Fatalf("migrated task %s %s", status, record)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record) VALUES (6, 1, 'app.task', 'sleeping', 0, 0, '{}')`); err != nil {
		t.Fatalf("v2 schema must accept sleeping: %v", err)
	}
}
