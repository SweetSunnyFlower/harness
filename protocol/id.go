package protocol

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"harness/jsonvalue"
)

const MaxSafeInteger uint64 = 1<<53 - 1

var ErrID = errors.New("ID must be a positive JavaScript safe integer")

type ID[K any] uint64

type recordKind struct{}
type conversationKind struct{}
type entryKind struct{}
type taskKind struct{}
type submissionKind struct{}
type documentKind struct{}
type sequenceKind struct{}

type RecordID = ID[recordKind]
type ConversationID = ID[conversationKind]
type EntryID = ID[entryKind]
type TaskID = ID[taskKind]
type SubmissionID = ID[submissionKind]
type DocumentID = ID[documentKind]
type Seq = ID[sequenceKind]

const RootConversationID ConversationID = 1

func (id ID[K]) Valid() bool {
	return uint64(id) >= 1 && uint64(id) <= MaxSafeInteger
}

func (id ID[K]) MarshalJSON() ([]byte, error) {
	if !id.Valid() {
		return nil, ErrID
	}
	return strconv.AppendUint(nil, uint64(id), 10), nil
}

func (id *ID[K]) UnmarshalJSON(data []byte) error {
	value, err := jsonvalue.Parse(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrID, err)
	}
	number, err := value.Number()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrID, err)
	}
	if number < 1 || number > float64(MaxSafeInteger) || math.Trunc(number) != number {
		return ErrID
	}
	*id = ID[K](uint64(number))
	return nil
}
