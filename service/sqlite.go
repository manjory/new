package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rainbowmga/timetravel/entity"

	// SQLite driver. mattn/go-sqlite3 requires CGo, so a C compiler (gcc /
	// clang / MSVC build tools) must be available when building.
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteRecordService is a SQLite-backed implementation of RecordService.
//
// Records are stored in a single table. The map of key/value data for a
// record is serialized to JSON and stored in a TEXT column. This keeps the
// schema very simple while preserving the existing API behavior.
type SQLiteRecordService struct {
	db *sql.DB
}

// NewSQLiteRecordService opens (or creates) a SQLite database at the given
// path and ensures the schema exists. The returned service is safe for
// concurrent use because *sql.DB manages its own connection pool.
func NewSQLiteRecordService(dbPath string) (*SQLiteRecordService, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database %q: %w", dbPath, err)
	}

	// Verify the connection is healthy before returning.
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging sqlite database: %w", err)
	}

	svc := &SQLiteRecordService{db: db}
	if err := svc.migrate(); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return svc, nil
}

// migrate creates the records table if it does not already exist.
func (s *SQLiteRecordService) migrate() error {
	const schema = `
        CREATE TABLE IF NOT EXISTS records (
            id   INTEGER PRIMARY KEY,
            data TEXT    NOT NULL
        );

        CREATE TABLE IF NOT EXISTS record_versions (
            record_id  INTEGER NOT NULL,
            version    INTEGER NOT NULL,
            data       TEXT    NOT NULL,
            created_at TEXT    NOT NULL,
            PRIMARY KEY (record_id, version)
        );

        CREATE INDEX IF NOT EXISTS idx_record_versions_record_id
            ON record_versions(record_id);
    `
	_, err := s.db.Exec(schema)
	return err
}

// Close releases the underlying database connection pool. Callers should
// invoke this on shutdown (e.g. with defer in main).
func (s *SQLiteRecordService) Close() error {
	return s.db.Close()
}

// GetRecord retrieves a record by id.
func (s *SQLiteRecordService) GetRecord(ctx context.Context, id int) (entity.Record, error) {
	var dataJSON string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT data FROM records WHERE id = ?`,
		id,
	).Scan(&dataJSON)

	if errors.Is(err, sql.ErrNoRows) {
		return entity.Record{}, ErrRecordDoesNotExist
	}
	if err != nil {
		return entity.Record{}, fmt.Errorf("querying record %d: %w", id, err)
	}

	data := map[string]string{}
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return entity.Record{}, fmt.Errorf("decoding record %d data: %w", id, err)
	}

	return entity.Record{ID: id, Data: data}, nil
}

// CreateRecord inserts a new record and records its initial version. It
// fails with ErrRecordAlreadyExists if a record with that id already
// exists.
//
// The insert into records and the insert of version 1 into record_versions
// happen in a single transaction so the history can never diverge from the
// latest-state cache.
func (s *SQLiteRecordService) CreateRecord(ctx context.Context, record entity.Record) error {
	if record.ID <= 0 {
		return ErrRecordIDInvalid
	}

	if record.Data == nil {
		record.Data = map[string]string{}
	}

	dataJSON, err := json.Marshal(record.Data)
	if err != nil {
		return fmt.Errorf("encoding record data: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Check for existence explicitly so we can return a clear sentinel
	// error rather than a driver-specific constraint message.
	var probe int
	err = tx.QueryRowContext(
		ctx,
		`SELECT 1 FROM records WHERE id = ?`,
		record.ID,
	).Scan(&probe)
	if err == nil {
		return ErrRecordAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("probing record %d: %w", record.ID, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO records (id, data) VALUES (?, ?)`,
		record.ID, string(dataJSON),
	); err != nil {
		return fmt.Errorf("inserting record %d: %w", record.ID, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO record_versions (record_id, version, data, created_at) VALUES (?, ?, ?, ?)`,
		record.ID, 1, string(dataJSON), nowUTC(),
	); err != nil {
		return fmt.Errorf("inserting version 1 of record %d: %w", record.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// UpdateRecord applies a partial update to an existing record. Keys whose
// value is nil are deleted from the record's data map. A new version is
// appended to record_versions on every successful update -- even if the
// resulting data is identical to the previous version. For an audit log,
// the act of writing is itself signal.
//
// The read-modify-write and the new version insert all run in a single
// transaction so that concurrent updates can't lose each other's writes
// and the history can't drift out of sync with the latest-state cache.
func (s *SQLiteRecordService) UpdateRecord(
	ctx context.Context,
	id int,
	updates map[string]*string,
) (entity.Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return entity.Record{}, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var dataJSON string
	err = tx.QueryRowContext(
		ctx,
		`SELECT data FROM records WHERE id = ?`,
		id,
	).Scan(&dataJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return entity.Record{}, ErrRecordDoesNotExist
	}
	if err != nil {
		return entity.Record{}, fmt.Errorf("reading record %d: %w", id, err)
	}

	data := map[string]string{}
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return entity.Record{}, fmt.Errorf("decoding record %d data: %w", id, err)
	}

	for key, value := range updates {
		if value == nil {
			delete(data, key)
		} else {
			data[key] = *value
		}
	}

	newJSON, err := json.Marshal(data)
	if err != nil {
		return entity.Record{}, fmt.Errorf("encoding record %d data: %w", id, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE records SET data = ? WHERE id = ?`,
		string(newJSON), id,
	); err != nil {
		return entity.Record{}, fmt.Errorf("updating record %d: %w", id, err)
	}

	// Determine the next version number for this record.
	var maxVersion sql.NullInt64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT MAX(version) FROM record_versions WHERE record_id = ?`,
		id,
	).Scan(&maxVersion); err != nil {
		return entity.Record{}, fmt.Errorf("reading max version for record %d: %w", id, err)
	}
	nextVersion := int(maxVersion.Int64) + 1

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO record_versions (record_id, version, data, created_at) VALUES (?, ?, ?, ?)`,
		id, nextVersion, string(newJSON), nowUTC(),
	); err != nil {
		return entity.Record{}, fmt.Errorf("inserting version %d of record %d: %w", nextVersion, id, err)
	}

	if err := tx.Commit(); err != nil {
		return entity.Record{}, fmt.Errorf("committing transaction: %w", err)
	}

	return entity.Record{ID: id, Data: data}, nil
}

// nowUTC returns the current time formatted as RFC3339 with nanosecond
// precision. Stored as TEXT so SQLite preserves it exactly and so dates
// sort lexicographically.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// GetRecordVersion returns a specific historical version of a record.
func (s *SQLiteRecordService) GetRecordVersion(
	ctx context.Context,
	id int,
	version int,
) (entity.VersionedRecord, error) {
	var dataJSON, createdAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT data, created_at FROM record_versions WHERE record_id = ? AND version = ?`,
		id, version,
	).Scan(&dataJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Distinguish "no such record at all" from "record exists, but not
		// at this version" so the API can return a more helpful error.
		var anyVersion int
		probeErr := s.db.QueryRowContext(
			ctx,
			`SELECT 1 FROM record_versions WHERE record_id = ? LIMIT 1`,
			id,
		).Scan(&anyVersion)
		if errors.Is(probeErr, sql.ErrNoRows) {
			return entity.VersionedRecord{}, ErrRecordDoesNotExist
		}
		return entity.VersionedRecord{}, ErrVersionDoesNotExist
	}
	if err != nil {
		return entity.VersionedRecord{}, fmt.Errorf("querying record %d version %d: %w", id, version, err)
	}

	data := map[string]string{}
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return entity.VersionedRecord{}, fmt.Errorf("decoding record %d version %d: %w", id, version, err)
	}

	ts, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return entity.VersionedRecord{}, fmt.Errorf("parsing created_at for record %d version %d: %w", id, version, err)
	}

	return entity.VersionedRecord{
		ID:        id,
		Version:   version,
		CreatedAt: ts,
		Data:      data,
	}, nil
}

// ListVersions returns all versions of a record, oldest first.
func (s *SQLiteRecordService) ListVersions(
	ctx context.Context,
	id int,
) ([]entity.VersionMeta, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT version, created_at FROM record_versions WHERE record_id = ? ORDER BY version ASC`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("listing versions of record %d: %w", id, err)
	}
	defer rows.Close()

	var versions []entity.VersionMeta
	for rows.Next() {
		var v int
		var createdAt string
		if err := rows.Scan(&v, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning version row for record %d: %w", id, err)
		}
		ts, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at for record %d version %d: %w", id, v, err)
		}
		versions = append(versions, entity.VersionMeta{Version: v, CreatedAt: ts})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating versions of record %d: %w", id, err)
	}

	if len(versions) == 0 {
		return nil, ErrRecordDoesNotExist
	}
	return versions, nil
}

// GetLatestVersion returns the highest-numbered version for a record.
// Returns ErrRecordDoesNotExist if the record id is unknown.
func (s *SQLiteRecordService) GetLatestVersion(ctx context.Context, id int) (entity.VersionedRecord, error) {
	var version int
	var dataJSON, createdAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT version, data, created_at FROM record_versions
		 WHERE record_id = ?
		 ORDER BY version DESC LIMIT 1`,
		id,
	).Scan(&version, &dataJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entity.VersionedRecord{}, ErrRecordDoesNotExist
	}
	if err != nil {
		return entity.VersionedRecord{}, fmt.Errorf("querying latest version of record %d: %w", id, err)
	}
	data := map[string]string{}
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return entity.VersionedRecord{}, fmt.Errorf("decoding record %d version %d: %w", id, version, err)
	}
	ts, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return entity.VersionedRecord{}, fmt.Errorf("parsing created_at for record %d version %d: %w", id, version, err)
	}
	return entity.VersionedRecord{ID: id, Version: version, CreatedAt: ts, Data: data}, nil
}
