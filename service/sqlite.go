
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging sqlite database: %w", err)
	}

	svc := &SQLiteRecordService{db: db}
	if err := svc.migrate(); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return svc, nil
}

func (s *SQLiteRecordService) migrate() error {
	const schema = `
        CREATE TABLE IF NOT EXISTS records (
            id   INTEGER PRIMARY KEY,
            data TEXT    NOT NULL
        );
    `
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteRecordService) Close() error {
	return s.db.Close()
}

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

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO records (id, data) VALUES (?, ?)`,
		record.ID, string(dataJSON),
	)
	if err != nil {
		var existing string
		probeErr := s.db.QueryRowContext(
			ctx,
			`SELECT data FROM records WHERE id = ?`,
			record.ID,
		).Scan(&existing)
		if probeErr == nil {
			return ErrRecordAlreadyExists
		}
		return fmt.Errorf("inserting record %d: %w", record.ID, err)
	}

	return nil
}

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

	if err := tx.Commit(); err != nil {
		return entity.Record{}, fmt.Errorf("committing transaction: %w", err)
	}

	return entity.Record{ID: id, Data: data}, nil
}