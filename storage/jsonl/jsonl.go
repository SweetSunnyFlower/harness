package jsonl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"harness/protocol"
	"harness/record"
	"harness/storage"
)

const (
	formatVersion = 1
	mainFile      = "main.jsonl"
)

// Store is a JSONL-backed Storage. It keeps the authoritative state in memory
// (via storage.Memory) and persists each commit as sidecar appends plus one
// main commit marker, matching pi-durable's JSONL format version 1.
type Store struct {
	mem                  *storage.Memory
	dir                  string
	fsync                bool
	currentOnlyDocuments map[protocol.DocumentID]bool
	liveTaskSidecars     map[protocol.TaskID]bool
}

func Open(dir string) (*Store, error) {
	return OpenWithOptions(dir, Options{})
}

type Options struct {
	FSync bool
}

func OpenWithOptions(dir string, opts Options) (*Store, error) {
	s := &Store{
		mem:                  storage.NewMemory(),
		dir:                  dir,
		fsync:                opts.FSync,
		currentOnlyDocuments: map[protocol.DocumentID]bool{},
		liveTaskSidecars:     map[protocol.TaskID]bool{},
	}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) MintID(ctx context.Context) (protocol.RecordID, error) {
	return s.mem.MintID(ctx)
}

func (s *Store) Commit(ctx context.Context, writes []storage.Write, adopt func(protocol.Seq)) (protocol.Seq, error) {
	p, err := s.mem.Prepare(writes)
	if err != nil {
		return 0, err
	}
	if err := s.persist(p.Seq(), p.Writes()); err != nil {
		return 0, err
	}
	seq := s.mem.Apply(p)
	adopt(seq)
	s.afterCommit(writes)
	return seq, nil
}

func isCurrentOnly(c record.DocumentCreate) bool {
	return c.Scope.Kind != "conversation" || c.History == "latest"
}

func (s *Store) afterCommit(writes []storage.Write) {
	for _, w := range writes {
		switch w := w.(type) {
		case storage.DocumentCreateWrite:
			if isCurrentOnly(w.Record) {
				s.currentOnlyDocuments[w.Record.ID] = true
			}
		case storage.TaskWrite:
			if w.Value.State.Status == "terminal" {
				delete(s.liveTaskSidecars, w.Value.ID)
			} else {
				s.liveTaskSidecars[w.Value.ID] = true
			}
		}
	}
	for _, w := range writes {
		switch w := w.(type) {
		case storage.DocumentRetireWrite:
			if s.currentOnlyDocuments[w.ID] {
				_ = os.Remove(filepath.Join(s.dir, sidecarName("doc", protocol.RecordID(w.ID))))
			}
		case storage.TaskWrite:
			if w.Value.State.Status == "terminal" {
				_ = os.Remove(filepath.Join(s.dir, sidecarName("task", protocol.RecordID(w.Value.ID))))
			}
		}
	}
}

func (s *Store) persist(seq protocol.Seq, writes []storage.Write) error {
	type mainOp struct {
		Type    string                 `json:"type"`
		Value   json.RawMessage        `json:"value,omitempty"`
		ID      protocol.DocumentID    `json:"id,omitempty"`
		Ordinal int                    `json:"ordinal,omitempty"`
		Record  *record.DocumentCreate `json:"record,omitempty"`
	}
	ops := make([]mainOp, 0, len(writes))
	sidecars := map[string][]byte{}
	nextOrdinal := 0
	for _, w := range writes {
		switch w := w.(type) {
		case storage.ConversationWrite:
			b, err := json.Marshal(w.Value)
			if err != nil {
				return err
			}
			ops = append(ops, mainOp{Type: "conversation", Value: b})
		case storage.EntryWrite:
			b, err := json.Marshal(w.Value)
			if err != nil {
				return err
			}
			ops = append(ops, mainOp{Type: "entry", Value: b})
		case storage.SubmissionWrite:
			b, err := json.Marshal(w.Value)
			if err != nil {
				return err
			}
			ops = append(ops, mainOp{Type: "submission", Value: b})
		case storage.TaskWrite:
			b, err := json.Marshal(w.Value)
			if err != nil {
				return err
			}
			if w.Value.State.Status == "completing" || w.Value.State.Status == "terminal" {
				ops = append(ops, mainOp{Type: "task", Value: b})
			} else {
				name := sidecarName("task", protocol.RecordID(w.Value.ID))
				ordinal := nextOrdinal
				nextOrdinal++
				sidecars[name] = append(sidecars[name], s.sidecarLine(seq, ordinal, map[string]any{"type": "task", "value": json.RawMessage(b)})...)
				ops = append(ops, mainOp{Type: "task.sidecar", ID: protocol.DocumentID(w.Value.ID), Ordinal: ordinal})
			}
		case storage.DocumentCreateWrite:
			content, err := json.Marshal(w.Content)
			if err != nil {
				return err
			}
			name := sidecarName("doc", protocol.RecordID(w.Record.ID))
			ordinal := nextOrdinal
			nextOrdinal++
			sidecars[name] = append(sidecars[name], s.sidecarLine(seq, ordinal, map[string]any{"type": "document", "id": w.Record.ID, "content": json.RawMessage(content)})...)
			rec := w.Record
			ops = append(ops, mainOp{Type: "document.create", Record: &rec, Ordinal: ordinal})
		case storage.DocumentChangeWrite:
			content, err := json.Marshal(w.Content)
			if err != nil {
				return err
			}
			name := sidecarName("doc", protocol.RecordID(w.ID))
			ordinal := nextOrdinal
			nextOrdinal++
			sidecars[name] = append(sidecars[name], s.sidecarLine(seq, ordinal, map[string]any{"type": "document", "id": w.ID, "content": json.RawMessage(content)})...)
			ops = append(ops, mainOp{Type: "document.change", ID: w.ID, Ordinal: ordinal})
		case storage.DocumentRetireWrite:
			ops = append(ops, mainOp{Type: "document.retire", ID: w.ID})
		default:
			return fmt.Errorf("jsonl: unknown write type %T", w)
		}
	}
	for name, data := range sidecars {
		if err := appendFile(filepath.Join(s.dir, name), data, s.fsync); err != nil {
			return err
		}
	}
	marker, err := json.Marshal(map[string]any{"format": formatVersion, "type": "commit", "seq": seq, "writes": ops})
	if err != nil {
		return err
	}
	return appendFile(filepath.Join(s.dir, mainFile), append(marker, '\n'), s.fsync)
}

func (s *Store) sidecarLine(seq protocol.Seq, ordinal int, payload any) []byte {
	b, _ := json.Marshal(map[string]any{"format": formatVersion, "type": "record", "seq": seq, "ordinal": ordinal, "payload": payload})
	return append(b, '\n')
}

func sidecarName(kind string, id protocol.RecordID) string {
	return fmt.Sprintf("%s-%d.jsonl", kind, id)
}

func appendFile(path string, data []byte, fsync bool) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if fsync {
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}

func (s *Store) replay() error {
	path := filepath.Join(s.dir, mainFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	type marker struct {
		Format int               `json:"format"`
		Type   string            `json:"type"`
		Seq    protocol.Seq      `json:"seq"`
		Writes []json.RawMessage `json:"writes"`
	}
	var markers []marker
	for scanner.Scan() {
		line := scanner.Bytes()
		var m marker
		if err := json.Unmarshal(line, &m); err != nil {
			// Torn final line: stop recovery at the last complete marker.
			break
		}
		if m.Format != formatVersion || m.Type != "commit" {
			return fmt.Errorf("jsonl: invalid commit marker")
		}
		markers = append(markers, m)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	records, err := s.loadSidecars()
	if err != nil {
		return err
	}
	currentOnly := map[protocol.DocumentID]bool{}
	retired := map[protocol.DocumentID]bool{}
	finalTaskLive := map[protocol.TaskID]bool{}
	for _, m := range markers {
		for _, raw := range m.Writes {
			var op struct {
				Type   string                `json:"type"`
				ID     protocol.DocumentID   `json:"id"`
				Record record.DocumentCreate `json:"record"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			switch op.Type {
			case "document.create":
				if isCurrentOnly(op.Record) {
					currentOnly[op.Record.ID] = true
				}
			case "document.retire":
				retired[op.ID] = true
			case "task":
				finalTaskLive[protocol.TaskID(op.ID)] = false
			case "task.sidecar":
				finalTaskLive[protocol.TaskID(op.ID)] = true
			}
		}
	}
	for _, m := range markers {
		writes, err := s.replayWrites(m.Seq, m.Writes, records, currentOnly, retired, finalTaskLive)
		if err != nil {
			return err
		}
		if _, err := s.mem.Commit(context.Background(), writes, func(protocol.Seq) {}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadSidecars() (map[string]map[string]json.RawMessage, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	records := map[string]map[string]json.RawMessage{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == mainFile {
			continue
		}
		if !strings.HasPrefix(e.Name(), "doc-") && !strings.HasPrefix(e.Name(), "task-") {
			continue
		}
		f, err := os.Open(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		for scanner.Scan() {
			var rec struct {
				Seq     protocol.Seq               `json:"seq"`
				Ordinal int                        `json:"ordinal"`
				Payload map[string]json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				f.Close()
				return nil, err
			}
			key := fmt.Sprintf("%s|%d|%d", e.Name(), rec.Seq, rec.Ordinal)
			fileRecords := records[e.Name()]
			if fileRecords == nil {
				fileRecords = map[string]json.RawMessage{}
				records[e.Name()] = fileRecords
			}
			for k, v := range rec.Payload {
				fileRecords[key+"|"+k] = v
			}
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func (s *Store) replayWrites(
	seq protocol.Seq,
	rawWrites []json.RawMessage,
	records map[string]map[string]json.RawMessage,
	currentOnly map[protocol.DocumentID]bool,
	retired map[protocol.DocumentID]bool,
	finalTaskLive map[protocol.TaskID]bool,
) ([]storage.Write, error) {
	out := make([]storage.Write, 0, len(rawWrites))
	for _, raw := range rawWrites {
		var op struct {
			Type    string                `json:"type"`
			Value   json.RawMessage       `json:"value"`
			ID      protocol.DocumentID   `json:"id"`
			Ordinal int                   `json:"ordinal"`
			Record  record.DocumentCreate `json:"record"`
		}
		if err := json.Unmarshal(raw, &op); err != nil {
			return nil, err
		}
		switch op.Type {
		case "conversation":
			var rec record.ConversationRecord
			if err := json.Unmarshal(op.Value, &rec); err != nil {
				return nil, err
			}
			out = append(out, storage.ConversationWrite{Value: rec})
		case "entry":
			var rec record.EntryRecord
			if err := json.Unmarshal(op.Value, &rec); err != nil {
				return nil, err
			}
			out = append(out, storage.EntryWrite{Value: rec})
		case "submission":
			var rec record.SubmissionRecord
			if err := json.Unmarshal(op.Value, &rec); err != nil {
				return nil, err
			}
			out = append(out, storage.SubmissionWrite{Value: rec})
		case "task":
			var rec record.TaskRecord
			if err := json.Unmarshal(op.Value, &rec); err != nil {
				return nil, err
			}
			out = append(out, storage.TaskWrite{Value: rec})
		case "task.sidecar":
			if live, ok := finalTaskLive[protocol.TaskID(op.ID)]; ok && !live {
				continue
			}
			key := fmt.Sprintf("%s|%d|%d|value", sidecarName("task", protocol.RecordID(op.ID)), seq, op.Ordinal)
			var rec record.TaskRecord
			if err := unmarshalSidecar(records, key, &rec); err != nil {
				return nil, err
			}
			out = append(out, storage.TaskWrite{Value: rec})
		case "document.create":
			reclaimed := retired[op.Record.ID] && currentOnly[op.Record.ID]
			key := fmt.Sprintf("%s|%d|%d|content", sidecarName("doc", protocol.RecordID(op.Record.ID)), seq, op.Ordinal)
			var content storage.DocumentContent
			if err := unmarshalSidecar(records, key, &content); err != nil {
				if reclaimed {
					content = storage.DocumentContent{Version: 1, Kind: "base", Value: map[string]any{}}
				} else {
					return nil, err
				}
			}
			out = append(out, storage.DocumentCreateWrite{Record: op.Record, Content: content})
		case "document.change":
			if retired[op.ID] && currentOnly[op.ID] {
				continue
			}
			key := fmt.Sprintf("%s|%d|%d|content", sidecarName("doc", protocol.RecordID(op.ID)), seq, op.Ordinal)
			var content storage.DocumentContent
			if err := unmarshalSidecar(records, key, &content); err != nil {
				return nil, err
			}
			out = append(out, storage.DocumentChangeWrite{ID: op.ID, Content: content})
		case "document.retire":
			out = append(out, storage.DocumentRetireWrite{ID: op.ID})
		default:
			return nil, fmt.Errorf("jsonl: unknown write type %q", op.Type)
		}
	}
	return out, nil
}

func unmarshalSidecar(records map[string]map[string]json.RawMessage, key string, into any) error {
	for _, fileRecords := range records {
		if raw, ok := fileRecords[key]; ok {
			return json.Unmarshal(raw, into)
		}
	}
	return fmt.Errorf("jsonl: missing sidecar record %s", key)
}

func (s *Store) Close(ctx context.Context) error {
	return s.mem.Close(ctx)
}

// ─── Storage delegation ──────────────────────────────────────────────────────

func (s *Store) Conversation(ctx context.Context, id protocol.ConversationID) (*record.ConversationRecord, error) {
	return s.mem.Conversation(ctx, id)
}
func (s *Store) ScanConversations(ctx context.Context, q storage.ConversationQuery, limit int, cursor storage.Cursor) (storage.Page[record.ConversationRecord], error) {
	return s.mem.ScanConversations(ctx, q, limit, cursor)
}
func (s *Store) Entry(ctx context.Context, id protocol.EntryID) (*storage.EntryAt, error) {
	return s.mem.Entry(ctx, id)
}
func (s *Store) ConversationEntry(ctx context.Context, conversationID protocol.ConversationID, id protocol.EntryID) (*storage.EntryAt, error) {
	return s.mem.ConversationEntry(ctx, conversationID, id)
}
func (s *Store) FindLatestHeadMarker(ctx context.Context, conversationID protocol.ConversationID, atOrBefore *protocol.EntryID) (*record.EntryRecord, error) {
	return s.mem.FindLatestHeadMarker(ctx, conversationID, atOrBefore)
}
func (s *Store) ScanEntries(ctx context.Context, q storage.EntryQuery, limit int, cursor storage.Cursor) (storage.Page[record.EntryRecord], error) {
	return s.mem.ScanEntries(ctx, q, limit, cursor)
}
func (s *Store) Task(ctx context.Context, id protocol.TaskID) (*record.TaskRecord, error) {
	return s.mem.Task(ctx, id)
}
func (s *Store) ScanTasks(ctx context.Context, q storage.TaskQuery, limit int, cursor storage.Cursor) (storage.Page[record.TaskRecord], error) {
	return s.mem.ScanTasks(ctx, q, limit, cursor)
}
func (s *Store) Submission(ctx context.Context, id protocol.SubmissionID) (*record.SubmissionRecord, error) {
	return s.mem.Submission(ctx, id)
}
func (s *Store) ScanSubmissions(ctx context.Context, q storage.SubmissionQuery, limit int, cursor storage.Cursor) (storage.Page[record.SubmissionRecord], error) {
	return s.mem.ScanSubmissions(ctx, q, limit, cursor)
}
func (s *Store) SubmissionByRequest(ctx context.Context, conversationID protocol.ConversationID, requestID string) (*record.SubmissionRecord, error) {
	return s.mem.SubmissionByRequest(ctx, conversationID, requestID)
}
func (s *Store) FindDocument(ctx context.Context, address storage.DocumentAddress, at storage.DocumentPoint) (*record.DocumentRecord, error) {
	return s.mem.FindDocument(ctx, address, at)
}
func (s *Store) Document(ctx context.Context, id protocol.DocumentID, at storage.DocumentPoint) (*storage.StoredDocument, error) {
	return s.mem.Document(ctx, id, at)
}
func (s *Store) ScanDocuments(ctx context.Context, q storage.DocumentQuery, limit int, cursor storage.Cursor) (storage.Page[record.DocumentRecord], error) {
	return s.mem.ScanDocuments(ctx, q, limit, cursor)
}
