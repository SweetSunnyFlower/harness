package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"harness/delta"
	"harness/protocol"
	"harness/record"
)

type documentRevision struct {
	content DocumentContent
	seq     protocol.Seq
}

type storedDocument struct {
	record    record.DocumentRecord
	revisions []documentRevision
}

type documentAddressIndex struct {
	ids       []protocol.DocumentID
	currentID *protocol.DocumentID
}

type memoryState struct {
	recordTypes              map[protocol.RecordID]string
	conversations            map[protocol.ConversationID]record.ConversationRecord
	conversationIDs          []protocol.ConversationID
	conversationsByOwnerConv map[protocol.ConversationID][]protocol.ConversationID
	conversationsByOwnerTask map[protocol.TaskID][]protocol.ConversationID
	entries                  map[protocol.EntryID]record.EntryRecord
	entryIDs                 map[protocol.ConversationID][]protocol.EntryID
	headEntryIDs             map[protocol.ConversationID][]protocol.EntryID
	entryCommitSeqs          map[protocol.EntryID]protocol.Seq
	tasks                    map[protocol.TaskID]record.TaskRecord
	taskIDs                  []protocol.TaskID
	taskIDsByStatus          map[string][]protocol.TaskID
	submissions              map[protocol.SubmissionID]record.SubmissionRecord
	submissionIDs            []protocol.SubmissionID
	submissionIDsByStatus    map[string][]protocol.SubmissionID
	submissionsByRequest     map[protocol.ConversationID]map[string]protocol.SubmissionID
	documents                map[protocol.DocumentID]*storedDocument
	documentAddresses        map[string]*documentAddressIndex
	documentIDsByScope       map[string][]protocol.DocumentID
}

type documentAction struct {
	create  *record.DocumentCreate
	content *DocumentContent
	retire  bool
}

// Memory is the detached in-memory reference Storage. Reads and retained
// writes are cloned to match the ownership boundary of serialization-backed
// stores. It is backend conformance, not validation.
type Memory struct {
	state   memoryState
	nextID  protocol.RecordID
	nextSeq protocol.Seq
	closed  bool
}

func NewMemory() *Memory {
	return &Memory{
		state: memoryState{
			recordTypes:              map[protocol.RecordID]string{},
			conversations:            map[protocol.ConversationID]record.ConversationRecord{},
			conversationsByOwnerConv: map[protocol.ConversationID][]protocol.ConversationID{},
			conversationsByOwnerTask: map[protocol.TaskID][]protocol.ConversationID{},
			entries:                  map[protocol.EntryID]record.EntryRecord{},
			entryIDs:                 map[protocol.ConversationID][]protocol.EntryID{},
			headEntryIDs:             map[protocol.ConversationID][]protocol.EntryID{},
			entryCommitSeqs:          map[protocol.EntryID]protocol.Seq{},
			tasks:                    map[protocol.TaskID]record.TaskRecord{},
			taskIDsByStatus:          map[string][]protocol.TaskID{},
			submissions:              map[protocol.SubmissionID]record.SubmissionRecord{},
			submissionIDsByStatus:    map[string][]protocol.SubmissionID{},
			submissionsByRequest:     map[protocol.ConversationID]map[string]protocol.SubmissionID{},
			documents:                map[protocol.DocumentID]*storedDocument{},
			documentAddresses:        map[string]*documentAddressIndex{},
			documentIDsByScope:       map[string][]protocol.DocumentID{},
		},
		nextID:  2,
		nextSeq: 1,
	}
}

func (m *Memory) Commit(ctx context.Context, writes []Write, adopt func(protocol.Seq)) (protocol.Seq, error) {
	if err := m.assertOpen(); err != nil {
		return 0, err
	}
	seq, err := m.prepareAndApply(writes)
	if err != nil {
		return 0, err
	}
	adopt(seq)
	return seq, nil
}

func (m *Memory) MintID(ctx context.Context) (protocol.RecordID, error) {
	if err := m.assertOpen(); err != nil {
		return 0, err
	}
	id := m.nextID
	m.nextID++
	return id, nil
}

func (m *Memory) prepareAndApply(writes []Write) (protocol.Seq, error) {
	detached, err := cloneWrites(writes)
	if err != nil {
		return 0, err
	}
	if err := m.checkGlobalIDs(detached); err != nil {
		return 0, err
	}
	resolved, err := m.resolveDocumentCopies(detached)
	if err != nil {
		return 0, err
	}
	actions, err := m.prepareDocumentActions(resolved)
	if err != nil {
		return 0, err
	}
	if err := m.checkDocumentActions(actions); err != nil {
		return 0, err
	}
	seq := m.nextSeq
	m.applyPrepared(resolved, actions, seq)
	m.nextSeq = seq + 1
	return seq, nil
}

func (m *Memory) applyPrepared(writes []Write, actions map[protocol.DocumentID]*documentAction, seq protocol.Seq) {
	for _, w := range writes {
		switch w := w.(type) {
		case ConversationWrite:
			id := w.Value.ID
			m.state.recordTypes[protocol.RecordID(id)] = "conversation"
			m.state.conversations[id] = w.Value
			insertSorted(&m.state.conversationIDs, id)
			if w.Value.Owner != nil {
				insertMapID(&m.state.conversationsByOwnerConv, w.Value.Owner.ConversationID, id)
				insertMapID(&m.state.conversationsByOwnerTask, w.Value.Owner.TaskID, id)
			}
			m.nextID = maxID(m.nextID, protocol.RecordID(id)+1)
		case EntryWrite:
			id := w.Value.ID
			m.state.recordTypes[protocol.RecordID(id)] = "entry"
			m.state.entries[id] = w.Value
			m.state.entryCommitSeqs[id] = seq
			ids := m.state.entryIDs[w.Value.ConversationID]
			insertSorted(&ids, id)
			m.state.entryIDs[w.Value.ConversationID] = ids
			if w.Value.Head != nil {
				heads := m.state.headEntryIDs[w.Value.ConversationID]
				insertSorted(&heads, id)
				m.state.headEntryIDs[w.Value.ConversationID] = heads
			}
			m.nextID = maxID(m.nextID, protocol.RecordID(id)+1)
		case TaskWrite:
			id := w.Value.ID
			m.state.recordTypes[protocol.RecordID(id)] = "task"
			prev, exists := m.state.tasks[id]
			if !exists {
				insertSorted(&m.state.taskIDs, id)
				insertMapID(&m.state.taskIDsByStatus, stringKey(w.Value.State.Status), id)
			} else if prev.State.Status != w.Value.State.Status {
				m.state.taskIDsByStatus[stringKey(prev.State.Status)] = removeSorted(m.state.taskIDsByStatus[stringKey(prev.State.Status)], id)
				insertMapID(&m.state.taskIDsByStatus, stringKey(w.Value.State.Status), id)
			}
			m.state.tasks[id] = w.Value
			m.nextID = maxID(m.nextID, protocol.RecordID(id)+1)
		case SubmissionWrite:
			id := w.Value.ID
			m.state.recordTypes[protocol.RecordID(id)] = "submission"
			prev, exists := m.state.submissions[id]
			if !exists {
				insertSorted(&m.state.submissionIDs, id)
				insertMapID(&m.state.submissionIDsByStatus, stringKey(w.Value.Status), id)
			} else if prev.Status != w.Value.Status {
				m.state.submissionIDsByStatus[stringKey(prev.Status)] = removeSorted(m.state.submissionIDsByStatus[stringKey(prev.Status)], id)
				insertMapID(&m.state.submissionIDsByStatus, stringKey(w.Value.Status), id)
			}
			if exists && prev.RequestID != "" {
				if byReq := m.state.submissionsByRequest[prev.ConversationID]; byReq != nil && byReq[prev.RequestID] == id {
					delete(byReq, prev.RequestID)
					if len(byReq) == 0 {
						delete(m.state.submissionsByRequest, prev.ConversationID)
					}
				}
			}
			m.state.submissions[id] = w.Value
			if w.Value.RequestID != "" {
				byReq := m.state.submissionsByRequest[w.Value.ConversationID]
				if byReq == nil {
					byReq = map[string]protocol.SubmissionID{}
					m.state.submissionsByRequest[w.Value.ConversationID] = byReq
				}
				byReq[w.Value.RequestID] = id
			}
			m.nextID = maxID(m.nextID, protocol.RecordID(id)+1)
		}
	}
	m.applyDocumentActions(actions, seq)
}

func (m *Memory) resolveDocumentCopies(writes []Write) ([]Write, error) {
	hasCopy := false
	changed := map[protocol.DocumentID]bool{}
	for _, w := range writes {
		switch w := w.(type) {
		case DocumentCreateWrite:
			changed[w.Record.ID] = true
		case DocumentCopyWrite:
			hasCopy = true
			changed[w.Record.ID] = true
		case DocumentChangeWrite:
			changed[w.ID] = true
		case DocumentRetireWrite:
			changed[w.ID] = true
		}
	}
	if !hasCopy {
		return writes, nil
	}
	out := make([]Write, 0, len(writes))
	for _, w := range writes {
		cw, ok := w.(DocumentCopyWrite)
		if !ok {
			out = append(out, w)
			continue
		}
		if changed[cw.Source.ID] {
			return nil, fmt.Errorf("fork source document %d is changed in the copy batch", cw.Source.ID)
		}
		stored, err := m.materialize(cw.Source.ID, cw.Source.At)
		if err != nil || stored == nil {
			return nil, fmt.Errorf("fork source document %d cannot be read", cw.Source.ID)
		}
		if stored.Record.Scope.Kind != "conversation" || cw.Record.Scope.Kind != "conversation" ||
			stored.Record.Kind != cw.Record.Kind || stored.Record.Key != cw.Record.Key ||
			stored.Record.History != cw.Record.History || stored.Record.Fork != cw.Record.Fork {
			return nil, fmt.Errorf("fork source document %d does not match the copied record", cw.Source.ID)
		}
		out = append(out, DocumentCreateWrite{
			Record:  cw.Record,
			Content: DocumentContent{Version: stored.Version, Kind: "base", Value: stored.Value},
		})
	}
	return out, nil
}

func (m *Memory) prepareDocumentActions(writes []Write) (map[protocol.DocumentID]*documentAction, error) {
	actions := map[protocol.DocumentID]*documentAction{}
	for _, w := range writes {
		var id protocol.DocumentID
		var create *record.DocumentCreate
		var content *DocumentContent
		var retire bool
		switch w := w.(type) {
		case DocumentCreateWrite:
			id = w.Record.ID
			rec := w.Record
			create = &rec
			c := w.Content
			content = &c
		case DocumentChangeWrite:
			id = w.ID
			c := w.Content
			content = &c
		case DocumentRetireWrite:
			id = w.ID
			retire = true
		default:
			continue
		}
		a := actions[id]
		if a == nil {
			a = &documentAction{}
			actions[id] = a
		}
		if create != nil {
			if a.create != nil || a.content != nil {
				return nil, fmt.Errorf("document %d has more than one content command", id)
			}
			a.create = create
			a.content = content
		}
		if content != nil && create == nil {
			if a.content != nil {
				return nil, fmt.Errorf("document %d has more than one content command", id)
			}
			a.content = content
		}
		if retire {
			if a.retire {
				return nil, fmt.Errorf("document %d is retired more than once", id)
			}
			a.retire = true
		}
	}
	return actions, nil
}

func (m *Memory) checkDocumentActions(actions map[protocol.DocumentID]*documentAction) error {
	type keyState struct {
		preCurrent     *protocol.DocumentID
		retiredCurrent bool
		newCurrents    int
	}
	states := map[string]*keyState{}
	for id, a := range actions {
		existing := m.state.documents[id]
		if a.create == nil && existing == nil {
			return fmt.Errorf("unknown document: %d", id)
		}
		if a.create != nil && existing != nil {
			return fmt.Errorf("document %d already exists", id)
		}
		if existing != nil && existing.record.RetiredAt != nil {
			return fmt.Errorf("document %d is retired", id)
		}
		if a.content != nil && a.content.Kind == "delta" {
			var prevVersion float64
			var hasPrev bool
			if existing != nil && len(existing.revisions) > 0 {
				prevVersion = existing.revisions[len(existing.revisions)-1].content.Version
				hasPrev = true
			}
			if !hasPrev {
				return fmt.Errorf("document %d delta has no base", id)
			}
			if prevVersion != a.content.Version {
				return fmt.Errorf("document %d version transition requires a base", id)
			}
		}
		var key string
		if a.create == nil {
			key = recordAddressKeyOf(existing.record)
		} else {
			key = recordAddressKeyOfCreate(*a.create)
		}
		ks := states[key]
		if ks == nil {
			ks = &keyState{}
			if idx := m.state.documentAddresses[key]; idx != nil {
				ks.preCurrent = idx.currentID
			}
			states[key] = ks
		}
		if a.retire && ks.preCurrent != nil && *ks.preCurrent == id {
			ks.retiredCurrent = true
		}
		if a.create != nil && !a.retire {
			ks.newCurrents++
		}
	}
	for _, ks := range states {
		live := 0
		if ks.preCurrent != nil && !ks.retiredCurrent {
			live = 1
		}
		live += ks.newCurrents
		if live > 1 {
			return fmt.Errorf("document address already has a current incarnation")
		}
	}
	return nil
}

func (m *Memory) applyDocumentActions(actions map[protocol.DocumentID]*documentAction, seq protocol.Seq) {
	for id, a := range actions {
		stored := m.state.documents[id]
		if a.create != nil {
			rec := documentRecordFromCreate(*a.create, seq)
			if a.retire {
				retired := seq
				rec.RetiredAt = &retired
			}
			stored = &storedDocument{record: rec, revisions: []documentRevision{{content: *a.content, seq: seq}}}
			m.state.recordTypes[protocol.RecordID(id)] = "document"
			m.state.documents[id] = stored

			key := recordAddressKeyOf(rec)
			addr := m.state.documentAddresses[key]
			if addr == nil {
				addr = &documentAddressIndex{}
				m.state.documentAddresses[key] = addr
			}
			insertSorted(&addr.ids, id)

			scope := scopeKeyOf(rec.Scope)
			insertMapID(&m.state.documentIDsByScope, scope, id)
			m.nextID = maxID(m.nextID, protocol.RecordID(id)+1)
		} else if a.content != nil {
			rev := documentRevision{content: *a.content, seq: seq}
			if rev.content.Kind == "base" && isCurrentOnly(stored.record) {
				stored.revisions = []documentRevision{rev}
			} else {
				stored.revisions = append(stored.revisions, rev)
			}
		}
		if a.retire && a.create == nil {
			retired := seq
			stored.record.RetiredAt = &retired
		}
		if a.retire && isCurrentOnly(stored.record) {
			stored.revisions = nil
		}
		if a.create != nil || a.retire {
			addr := m.state.documentAddresses[recordAddressKeyOf(stored.record)]
			if a.retire && addr.currentID != nil && *addr.currentID == id {
				addr.currentID = nil
			}
			if a.create != nil && !a.retire {
				cid := id
				addr.currentID = &cid
			}
		}
	}
}

func (m *Memory) checkGlobalIDs(writes []Write) error {
	claimed := map[protocol.RecordID]string{}
	for _, w := range writes {
		table, id, ok := writeIdentity(w)
		if !ok {
			continue
		}
		existing := m.state.recordTypes[id]
		earlier := claimed[id]
		if table == "conversation" || table == "entry" || table == "document" {
			if existing != "" {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" {
				return fmt.Errorf("ID %d is written more than once", id)
			}
		} else {
			if existing != "" && existing != table {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" && earlier != table {
				return fmt.Errorf("ID %d is written as two record types", id)
			}
		}
		claimed[id] = table
	}
	return nil
}

func writeIdentity(w Write) (table string, id protocol.RecordID, ok bool) {
	switch w := w.(type) {
	case ConversationWrite:
		return "conversation", protocol.RecordID(w.Value.ID), true
	case EntryWrite:
		return "entry", protocol.RecordID(w.Value.ID), true
	case TaskWrite:
		return "task", protocol.RecordID(w.Value.ID), true
	case SubmissionWrite:
		return "submission", protocol.RecordID(w.Value.ID), true
	case DocumentCreateWrite:
		return "document", protocol.RecordID(w.Record.ID), true
	case DocumentCopyWrite:
		return "document", protocol.RecordID(w.Record.ID), true
	}
	return "", 0, false
}

func cloneWrites(writes []Write) ([]Write, error) {
	out := make([]Write, 0, len(writes))
	for _, w := range writes {
		switch w := w.(type) {
		case ConversationWrite:
			c, err := cloneViaJSON(w.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, ConversationWrite{Value: c})
		case EntryWrite:
			c, err := cloneViaJSON(w.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, EntryWrite{Value: c})
		case TaskWrite:
			c, err := cloneViaJSON(w.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, TaskWrite{Value: c})
		case SubmissionWrite:
			c, err := cloneViaJSON(w.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, SubmissionWrite{Value: c})
		case DocumentCreateWrite:
			rec, err := cloneViaJSON(w.Record)
			if err != nil {
				return nil, err
			}
			content, err := cloneViaJSON(w.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, DocumentCreateWrite{Record: rec, Content: content})
		case DocumentCopyWrite:
			rec, err := cloneViaJSON(w.Record)
			if err != nil {
				return nil, err
			}
			out = append(out, DocumentCopyWrite{Record: rec, Source: w.Source})
		case DocumentChangeWrite:
			content, err := cloneViaJSON(w.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, DocumentChangeWrite{ID: w.ID, Content: content})
		case DocumentRetireWrite:
			out = append(out, DocumentRetireWrite{ID: w.ID})
		default:
			return nil, fmt.Errorf("storage: unknown write type %T", w)
		}
	}
	return out, nil
}

func cloneViaJSON[T any](v T) (T, error) {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	return out, nil
}

func documentRecordFromCreate(c record.DocumentCreate, seq protocol.Seq) record.DocumentRecord {
	return record.DocumentRecordFromCreate(c, seq)
}

func isCurrentOnly(r record.DocumentRecord) bool {
	return r.Scope.Kind != "conversation" || r.History == "latest"
}

func scopeKeyOf(scope record.DocumentScope) string {
	switch scope.Kind {
	case "session":
		return `["session"]`
	case "conversation":
		return fmt.Sprintf(`["conversation",%d]`, uint64(*scope.ConversationID))
	case "task":
		return fmt.Sprintf(`["task",%d]`, uint64(*scope.TaskID))
	}
	return ""
}

func recordAddressKeyOf(r record.DocumentRecord) string {
	var key *string
	if r.Key != "" {
		k := r.Key
		key = &k
	}
	return addressKeyString(r.Kind, r.Scope, key)
}

func recordAddressKeyOfCreate(c record.DocumentCreate) string {
	var key *string
	if c.Key != "" {
		k := c.Key
		key = &k
	}
	return addressKeyString(c.Kind, c.Scope, key)
}

func addressKeyString(kind string, scope record.DocumentScope, key *string) string {
	if key != nil {
		return fmt.Sprintf(`[%q,%s,["family",%q]]`, kind, scopeKeyOf(scope), *key)
	}
	return fmt.Sprintf(`[%q,%s,["singleton"]]`, kind, scopeKeyOf(scope))
}

func (m *Memory) materialize(id protocol.DocumentID, at DocumentPoint) (*StoredDocument, error) {
	stored := m.state.documents[id]
	if stored == nil {
		return nil, nil
	}
	if !at.IsCurrent() && isCurrentOnly(stored.record) {
		return nil, fmt.Errorf("%w: document %d does not retain historical content", ErrStorageRequest, id)
	}
	if !aliveAt(stored.record, at) {
		return nil, nil
	}
	revisions := stored.revisions
	if !at.IsCurrent() {
		revs := make([]documentRevision, 0, len(revisions))
		for _, r := range revisions {
			if uint64(r.seq) <= uint64(at.Seq()) {
				revs = append(revs, r)
			}
		}
		revisions = revs
	}
	baseIndex := -1
	for i := len(revisions) - 1; i >= 0; i-- {
		if revisions[i].content.Kind == "base" {
			baseIndex = i
			break
		}
	}
	if baseIndex < 0 {
		return nil, fmt.Errorf("document %d is missing a required base", id)
	}
	base := revisions[baseIndex]
	batches := make([][]delta.Op, 0, len(revisions)-baseIndex-1)
	for _, r := range revisions[baseIndex+1:] {
		if r.content.Kind != "delta" || r.content.Version != base.content.Version {
			return nil, fmt.Errorf("document %d crosses a stored version boundary without a base", id)
		}
		batches = append(batches, r.content.Ops)
	}
	value, err := delta.ApplyImmutableBatches(base.content.Value, batches)
	if err != nil {
		return nil, err
	}
	value, err = cloneViaJSON(value)
	if err != nil {
		return nil, err
	}
	rec, err := cloneViaJSON(stored.record)
	if err != nil {
		return nil, err
	}
	return &StoredDocument{
		Record:          rec,
		Version:         base.content.Version,
		Value:           value,
		DeltasSinceBase: len(revisions) - baseIndex - 1,
	}, nil
}

func aliveAt(r record.DocumentRecord, at DocumentPoint) bool {
	if at.IsCurrent() {
		return r.RetiredAt == nil
	}
	atSeq := uint64(at.Seq())
	return uint64(r.CreatedAt) <= atSeq && (r.RetiredAt == nil || atSeq < uint64(*r.RetiredAt))
}

func (m *Memory) assertOpen() error {
	if m.closed {
		return fmt.Errorf("storage: memory store is closed")
	}
	return nil
}

// ─── Reads ──────────────────────────────────────────────────────────────────

func (m *Memory) Conversation(ctx context.Context, id protocol.ConversationID) (*record.ConversationRecord, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	v, ok := m.state.conversations[id]
	if !ok {
		return nil, nil
	}
	c, err := cloneViaJSON(v)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *Memory) ScanConversations(ctx context.Context, q ConversationQuery, limit int, cursor Cursor) (Page[record.ConversationRecord], error) {
	if err := m.assertOpen(); err != nil {
		return Page[record.ConversationRecord]{}, err
	}
	order, after, err := scanStart(q.Order, cursor, Ascending)
	if err != nil {
		return Page[record.ConversationRecord]{}, err
	}
	var ids []protocol.ConversationID
	switch {
	case q.OwnerTaskID != nil:
		ids = m.state.conversationsByOwnerTask[*q.OwnerTaskID]
	case q.OwnerConversationID != nil:
		ids = m.state.conversationsByOwnerConv[*q.OwnerConversationID]
	default:
		ids = m.state.conversationIDs
	}
	values := make([]record.ConversationRecord, 0)
	var collectedIDs []protocol.RecordID
	for _, id := range scanIDs(ids, order, after) {
		if len(values) > limit {
			break
		}
		v := m.state.conversations[id]
		if q.OwnerConversationID != nil && (v.Owner == nil || v.Owner.ConversationID != *q.OwnerConversationID) {
			continue
		}
		c, err := cloneViaJSON(v)
		if err != nil {
			return Page[record.ConversationRecord]{}, err
		}
		values = append(values, c)
		collectedIDs = append(collectedIDs, protocol.RecordID(id))
	}
	return makePage(values, collectedIDs, limit, order), nil
}

func (m *Memory) Entry(ctx context.Context, id protocol.EntryID) (*EntryAt, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	entry, ok := m.state.entries[id]
	if !ok {
		return nil, nil
	}
	c, err := cloneViaJSON(entry)
	if err != nil {
		return nil, err
	}
	return &EntryAt{Entry: c, CommitSeq: m.state.entryCommitSeqs[id]}, nil
}

func (m *Memory) ConversationEntry(ctx context.Context, conversationID protocol.ConversationID, id protocol.EntryID) (*EntryAt, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	var found *record.EntryRecord
	m.visibleEntries(conversationID, uint64(id), uint64(id), func(e record.EntryRecord) bool {
		entry := e
		found = &entry
		return false
	})
	if found != nil {
		entry := *found
		c, err := cloneViaJSON(entry)
		if err != nil {
			return nil, err
		}
		return &EntryAt{Entry: c, CommitSeq: m.state.entryCommitSeqs[id]}, nil
	}
	return nil, nil
}

func (m *Memory) FindLatestHeadMarker(ctx context.Context, conversationID protocol.ConversationID, atOrBefore *protocol.EntryID) (*record.EntryRecord, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	if _, ok := m.state.conversations[conversationID]; !ok {
		return nil, fmt.Errorf("%w: unknown conversation %d", ErrStorageRequest, conversationID)
	}
	current := conversationID
	upper := uint64(^uint64(0))
	if atOrBefore != nil {
		upper = uint64(*atOrBefore)
	}
	for {
		ids := m.state.headEntryIDs[current]
		idx := upperBound(ids, upper) - 1
		if idx >= 0 {
			c, err := cloneViaJSON(m.state.entries[ids[idx]])
			if err != nil {
				return nil, err
			}
			return &c, nil
		}
		conversation := m.state.conversations[current]
		if conversation.Parent == nil {
			return nil, nil
		}
		upper = minU64(upper, uint64(conversation.Parent.At))
		current = conversation.Parent.ConversationID
	}
}

func (m *Memory) ScanEntries(ctx context.Context, q EntryQuery, limit int, cursor Cursor) (Page[record.EntryRecord], error) {
	if err := m.assertOpen(); err != nil {
		return Page[record.EntryRecord]{}, err
	}
	order, after, err := scanStart(q.Order, cursor, Descending)
	if err != nil {
		return Page[record.EntryRecord]{}, err
	}
	minID, maxID := minMaxEntryIDs(q, order, after)
	var it func(func(record.EntryRecord) bool)
	if order == Descending {
		it = func(yield func(record.EntryRecord) bool) {
			m.visibleEntries(q.ConversationID, minID, maxID, yield)
		}
	} else {
		it = func(yield func(record.EntryRecord) bool) {
			m.visibleEntriesAscending(q.ConversationID, minID, maxID, yield)
		}
	}
	values := make([]record.EntryRecord, 0)
	var collectedIDs []protocol.RecordID
	it(func(e record.EntryRecord) bool {
		if len(values) > limit {
			return false
		}
		c, err := cloneViaJSON(e)
		if err != nil {
			return false
		}
		values = append(values, c)
		collectedIDs = append(collectedIDs, protocol.RecordID(e.ID))
		return true
	})
	return makePage(values, collectedIDs, limit, order), nil
}

func minMaxEntryIDs(q EntryQuery, order ScanOrder, after *protocol.RecordID) (minID, maxID uint64) {
	minID = 0
	maxID = ^uint64(0)
	if q.MinEntryID != nil {
		minID = uint64(*q.MinEntryID)
	}
	if q.MaxEntryID != nil {
		maxID = uint64(*q.MaxEntryID)
	}
	if after != nil {
		if order == Descending {
			maxID = minU64(maxID, uint64(*after)-1)
		} else {
			minID = maxU64(minID, uint64(*after)+1)
		}
	}
	return minID, maxID
}

func (m *Memory) visibleEntries(conversationID protocol.ConversationID, minID, maxID uint64, yield func(record.EntryRecord) bool) {
	if _, ok := m.state.conversations[conversationID]; !ok {
		return
	}
	current := conversationID
	upper := maxID
	for {
		ids := m.state.entryIDs[current]
		for i := upperBound(ids, upper) - 1; i >= 0; i-- {
			id := ids[i]
			if uint64(id) < minID {
				break
			}
			if !yield(m.state.entries[id]) {
				return
			}
		}
		conversation := m.state.conversations[current]
		if conversation.Parent == nil {
			return
		}
		upper = minU64(upper, uint64(conversation.Parent.At))
		if upper < minID {
			return
		}
		current = conversation.Parent.ConversationID
	}
}

func (m *Memory) visibleEntriesAscending(conversationID protocol.ConversationID, minID, maxID uint64, yield func(record.EntryRecord) bool) {
	if _, ok := m.state.conversations[conversationID]; !ok {
		return
	}
	type segment struct {
		conversationID protocol.ConversationID
		upper          uint64
	}
	segments := make([]segment, 0, 4)
	current := conversationID
	upper := maxID
	for {
		segments = append(segments, segment{current, upper})
		conversation := m.state.conversations[current]
		if conversation.Parent == nil {
			break
		}
		upper = minU64(upper, uint64(conversation.Parent.At))
		if upper < minID {
			break
		}
		current = conversation.Parent.ConversationID
	}
	for i := len(segments) - 1; i >= 0; i-- {
		seg := segments[i]
		ids := m.state.entryIDs[seg.conversationID]
		for j := lowerBound(ids, minID); j < len(ids); j++ {
			id := ids[j]
			if uint64(id) > seg.upper {
				break
			}
			if !yield(m.state.entries[id]) {
				return
			}
		}
	}
}

func (m *Memory) Task(ctx context.Context, id protocol.TaskID) (*record.TaskRecord, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	v, ok := m.state.tasks[id]
	if !ok {
		return nil, nil
	}
	c, err := cloneViaJSON(v)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *Memory) ScanTasks(ctx context.Context, q TaskQuery, limit int, cursor Cursor) (Page[record.TaskRecord], error) {
	if err := m.assertOpen(); err != nil {
		return Page[record.TaskRecord]{}, err
	}
	order, after, err := scanStart(q.Order, cursor, Ascending)
	if err != nil {
		return Page[record.TaskRecord]{}, err
	}
	ids := m.state.taskIDs
	if q.Status != "" {
		ids = m.state.taskIDsByStatus[stringKey(q.Status)]
	}
	values := make([]record.TaskRecord, 0)
	var collectedIDs []protocol.RecordID
	for _, id := range scanIDs(ids, order, after) {
		if len(values) > limit {
			break
		}
		v := m.state.tasks[id]
		if q.ConversationID != nil && v.ConversationID != *q.ConversationID {
			continue
		}
		if q.Kind != "" && v.Kind != q.Kind {
			continue
		}
		if q.AbortRequested != nil && v.AbortRequested != *q.AbortRequested {
			continue
		}
		if q.Background != nil && v.Background != *q.Background {
			continue
		}
		c, err := cloneViaJSON(v)
		if err != nil {
			return Page[record.TaskRecord]{}, err
		}
		values = append(values, c)
		collectedIDs = append(collectedIDs, protocol.RecordID(id))
	}
	return makePage(values, collectedIDs, limit, order), nil
}

func (m *Memory) Submission(ctx context.Context, id protocol.SubmissionID) (*record.SubmissionRecord, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	v, ok := m.state.submissions[id]
	if !ok {
		return nil, nil
	}
	c, err := cloneViaJSON(v)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *Memory) ScanSubmissions(ctx context.Context, q SubmissionQuery, limit int, cursor Cursor) (Page[record.SubmissionRecord], error) {
	if err := m.assertOpen(); err != nil {
		return Page[record.SubmissionRecord]{}, err
	}
	order, after, err := scanStart(q.Order, cursor, Ascending)
	if err != nil {
		return Page[record.SubmissionRecord]{}, err
	}
	ids := m.state.submissionIDs
	if q.Status != "" {
		ids = m.state.submissionIDsByStatus[stringKey(q.Status)]
	}
	values := make([]record.SubmissionRecord, 0)
	var collectedIDs []protocol.RecordID
	for _, id := range scanIDs(ids, order, after) {
		if len(values) > limit {
			break
		}
		v := m.state.submissions[id]
		if q.ConversationID != nil && v.ConversationID != *q.ConversationID {
			continue
		}
		c, err := cloneViaJSON(v)
		if err != nil {
			return Page[record.SubmissionRecord]{}, err
		}
		values = append(values, c)
		collectedIDs = append(collectedIDs, protocol.RecordID(id))
	}
	return makePage(values, collectedIDs, limit, order), nil
}

func (m *Memory) SubmissionByRequest(ctx context.Context, conversationID protocol.ConversationID, requestID string) (*record.SubmissionRecord, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	id, ok := m.state.submissionsByRequest[conversationID][requestID]
	if !ok {
		return nil, nil
	}
	c, err := cloneViaJSON(m.state.submissions[id])
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *Memory) FindDocument(ctx context.Context, address DocumentAddress, at DocumentPoint) (*record.DocumentRecord, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	index := m.state.documentAddresses[addressKeyString(address.Kind, address.Scope, address.Key)]
	if index == nil {
		return nil, nil
	}
	if at.IsCurrent() {
		if index.currentID == nil {
			return nil, nil
		}
		rec, err := cloneViaJSON(m.state.documents[*index.currentID].record)
		if err != nil {
			return nil, err
		}
		return &rec, nil
	}
	for _, id := range index.ids {
		rec := m.state.documents[id].record
		if aliveAt(rec, at) {
			c, err := cloneViaJSON(rec)
			if err != nil {
				return nil, err
			}
			return &c, nil
		}
	}
	return nil, nil
}

func (m *Memory) Document(ctx context.Context, id protocol.DocumentID, at DocumentPoint) (*StoredDocument, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	return m.materialize(id, at)
}

func (m *Memory) ScanDocuments(ctx context.Context, q DocumentQuery, limit int, cursor Cursor) (Page[record.DocumentRecord], error) {
	if err := m.assertOpen(); err != nil {
		return Page[record.DocumentRecord]{}, err
	}
	ids := m.state.documentIDsByScope[scopeKeyOf(q.Scope)]
	_, after, err := scanStart(Ascending, cursor, Ascending)
	if err != nil {
		return Page[record.DocumentRecord]{}, err
	}
	start := 0
	if after != nil {
		start = upperBound(ids, uint64(*after))
	}
	values := make([]record.DocumentRecord, 0)
	var collectedIDs []protocol.RecordID
	for i := start; i < len(ids) && len(values) <= limit; i++ {
		rec := m.state.documents[ids[i]].record
		if q.Kind != "" && rec.Kind != q.Kind {
			continue
		}
		if !aliveAt(rec, q.At) {
			continue
		}
		c, err := cloneViaJSON(rec)
		if err != nil {
			return Page[record.DocumentRecord]{}, err
		}
		values = append(values, c)
		collectedIDs = append(collectedIDs, protocol.RecordID(ids[i]))
	}
	return makePage(values, collectedIDs, limit, Ascending), nil
}

func (m *Memory) Close(ctx context.Context) error {
	m.closed = true
	return nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func makePage[T any](values []T, ids []protocol.RecordID, limit int, order ScanOrder) Page[T] {
	if len(values) <= limit {
		return Page[T]{Items: values}
	}
	return Page[T]{Items: values[:limit], Next: nextCursor(ids[limit-1], order)}
}

func nextCursor(id protocol.RecordID, order ScanOrder) Cursor {
	b, _ := json.Marshal(map[string]any{"after": id, "order": order})
	return b
}

func scanStart(requested ScanOrder, cursor Cursor, fallback ScanOrder) (ScanOrder, *protocol.RecordID, error) {
	order := requested
	if order == "" {
		order = fallback
	}
	if order != Ascending && order != Descending {
		return "", nil, fmt.Errorf("%w: invalid scan order %q", ErrStorageRequest, order)
	}
	if len(cursor) == 0 {
		return order, nil, nil
	}
	var c struct {
		After protocol.RecordID `json:"after"`
		Order ScanOrder         `json:"order"`
	}
	if err := json.Unmarshal(cursor, &c); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrStorageRequest, err)
	}
	if c.Order != "" {
		if requested != "" && requested != c.Order {
			return "", nil, fmt.Errorf("%w: cursor continues a %s scan; query asks for %s", ErrStorageRequest, c.Order, requested)
		}
		order = c.Order
	}
	after := c.After
	return order, &after, nil
}

func scanIDs[I ~uint64](ids []I, order ScanOrder, after *protocol.RecordID) []I {
	if after == nil {
		if order == Descending {
			out := make([]I, 0, len(ids))
			for i := len(ids) - 1; i >= 0; i-- {
				out = append(out, ids[i])
			}
			return out
		}
		return ids
	}
	target := uint64(*after)
	if order == Descending {
		end := lowerBound(ids, target)
		out := make([]I, 0, end)
		for i := end - 1; i >= 0; i-- {
			out = append(out, ids[i])
		}
		return out
	}
	start := upperBound(ids, target)
	return ids[start:]
}

func lowerBound[I ~uint64](ids []I, target uint64) int {
	return sort.Search(len(ids), func(i int) bool { return uint64(ids[i]) >= target })
}

func upperBound[I ~uint64](ids []I, target uint64) int {
	return sort.Search(len(ids), func(i int) bool { return uint64(ids[i]) > target })
}

func insertSorted[I ~uint64](ids *[]I, id I) {
	i := lowerBound(*ids, uint64(id))
	*ids = append(*ids, 0)
	copy((*ids)[i+1:], (*ids)[i:])
	(*ids)[i] = id
}

func removeSorted[I ~uint64](ids []I, id I) []I {
	i := lowerBound(ids, uint64(id))
	if i < len(ids) && uint64(ids[i]) == uint64(id) {
		return append(ids[:i], ids[i+1:]...)
	}
	return ids
}

func insertMapID[K comparable, I ~uint64](index *map[K][]I, key K, id I) {
	if *index == nil {
		*index = map[K][]I{}
	}
	ids := (*index)[key]
	insertSorted(&ids, id)
	(*index)[key] = ids
}

func stringKey(s string) string { return s }

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func maxID(a, b protocol.RecordID) protocol.RecordID {
	if a > b {
		return a
	}
	return b
}
