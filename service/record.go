package service

import (
	"context"
	"errors"

	"github.com/rainbowmga/timetravel/entity"
)

var ErrRecordDoesNotExist = errors.New("record with that id does not exist")
var ErrRecordIDInvalid = errors.New("record id must >= 0")
var ErrRecordAlreadyExists = errors.New("record already exists")
var ErrVersionDoesNotExist = errors.New("record version does not exist")

// Implements method to get, create, and update record data.
type RecordService interface {

	// GetRecord will retrieve an record.
	GetRecord(ctx context.Context, id int) (entity.Record, error)

	// CreateRecord will insert a new record.
	//
	// If it a record with that id already exists it will fail.
	CreateRecord(ctx context.Context, record entity.Record) error

	// UpdateRecord will change the internal `Map` values of the record if they exist.
	// if the update[key] is null it will delete that key from the record's Map.
	//
	// UpdateRecord will error if id <= 0 or the record does not exist with that id.
	UpdateRecord(ctx context.Context, id int, updates map[string]*string) (entity.Record, error)
}

// VersionedRecordService extends RecordService with read access to a
// record's version history. Writes still go through the embedded
// RecordService methods; the underlying implementation is responsible for
// appending a new version on every successful write.
//
// Splitting this from RecordService keeps the v1 API package depending only
// on the smaller interface and unaware that versioning exists.
type VersionedRecordService interface {
	RecordService

	// GetRecordVersion returns a specific historical version of a record.
	// Returns ErrRecordDoesNotExist if the record id is unknown, or
	// ErrVersionDoesNotExist if the record exists but that version does not.
	GetRecordVersion(ctx context.Context, id int, version int) (entity.VersionedRecord, error)

	// ListVersions returns all versions of a record in ascending order
	// (oldest first). Returns ErrRecordDoesNotExist if the record id is
	// unknown.
	ListVersions(ctx context.Context, id int) ([]entity.VersionMeta, error)

	// GetLatestVersion returns the most recent version of a record. It is
	// equivalent to GetRecord but returns the richer VersionedRecord shape,
	// so v2 callers don't need a second round trip to learn the version
	// number / timestamp of what they just read.
	GetLatestVersion(ctx context.Context, id int) (entity.VersionedRecord, error)
}

// InMemoryRecordService is an in-memory implementation of RecordService.
type InMemoryRecordService struct {
	data map[int]entity.Record
}

func NewInMemoryRecordService() InMemoryRecordService {
	return InMemoryRecordService{
		data: map[int]entity.Record{},
	}
}

func (s *InMemoryRecordService) GetRecord(ctx context.Context, id int) (entity.Record, error) {
	record := s.data[id]
	if record.ID == 0 {
		return entity.Record{}, ErrRecordDoesNotExist
	}

	record = record.Copy() // copy is necessary so modifations to the record don't change the stored record
	return record, nil
}

func (s *InMemoryRecordService) CreateRecord(ctx context.Context, record entity.Record) error {
	id := record.ID
	if id <= 0 {
		return ErrRecordIDInvalid
	}

	existingRecord := s.data[id]
	if existingRecord.ID != 0 {
		return ErrRecordAlreadyExists
	}

	s.data[id] = record
	return nil
}

func (s *InMemoryRecordService) UpdateRecord(ctx context.Context, id int, updates map[string]*string) (entity.Record, error) {
	entry := s.data[id]
	if entry.ID == 0 {
		return entity.Record{}, ErrRecordDoesNotExist
	}

	for key, value := range updates {
		if value == nil { // deletion update
			delete(entry.Data, key)
		} else {
			entry.Data[key] = *value
		}
	}

	return entry.Copy(), nil
}
