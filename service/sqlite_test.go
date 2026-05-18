package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rainbowmga/timetravel/entity"
)

func TestVersioning(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ctx := context.Background()

	svc, err := NewSQLiteRecordService(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer svc.Close()

	// Create -> version 1
	if err := svc.CreateRecord(ctx, entity.Record{
		ID:   42,
		Data: map[string]string{"a": "1"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update -> version 2
	v := "2"
	if _, err := svc.UpdateRecord(ctx, 42, map[string]*string{"a": &v}); err != nil {
		t.Fatalf("update v2: %v", err)
	}

	// Update -> version 3 (add field)
	hello := "world"
	if _, err := svc.UpdateRecord(ctx, 42, map[string]*string{"hello": &hello}); err != nil {
		t.Fatalf("update v3: %v", err)
	}

	// Update -> version 4 (delete field)
	if _, err := svc.UpdateRecord(ctx, 42, map[string]*string{"a": nil}); err != nil {
		t.Fatalf("update v4: %v", err)
	}

	// Identical update -> version 5 (we still record no-ops by design)
	if _, err := svc.UpdateRecord(ctx, 42, map[string]*string{"hello": &hello}); err != nil {
		t.Fatalf("update v5: %v", err)
	}

	// List versions: expect 5
	versions, err := svc.ListVersions(ctx, 42)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 5 {
		t.Fatalf("expected 5 versions, got %d: %+v", len(versions), versions)
	}
	for i, vm := range versions {
		if vm.Version != i+1 {
			t.Fatalf("versions not sequential at index %d: %+v", i, versions)
		}
		if vm.CreatedAt.IsZero() {
			t.Fatalf("missing created_at at version %d", vm.Version)
		}
	}

	// Read version 1 -> {a: "1"}
	v1, err := svc.GetRecordVersion(ctx, 42, 1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if v1.Data["a"] != "1" || len(v1.Data) != 1 {
		t.Fatalf("unexpected v1 data: %+v", v1)
	}
	if v1.Version != 1 || v1.ID != 42 {
		t.Fatalf("wrong id/version on v1: %+v", v1)
	}

	// Read version 3 -> {a: "2", hello: "world"}
	v3, err := svc.GetRecordVersion(ctx, 42, 3)
	if err != nil {
		t.Fatalf("get v3: %v", err)
	}
	if v3.Data["a"] != "2" || v3.Data["hello"] != "world" || len(v3.Data) != 2 {
		t.Fatalf("unexpected v3 data: %+v", v3)
	}

	// Read version 4 -> {hello: "world"} (a deleted)
	v4, err := svc.GetRecordVersion(ctx, 42, 4)
	if err != nil {
		t.Fatalf("get v4: %v", err)
	}
	if _, present := v4.Data["a"]; present {
		t.Fatalf("expected a to be absent in v4: %+v", v4)
	}
	if v4.Data["hello"] != "world" {
		t.Fatalf("expected hello=world in v4: %+v", v4)
	}

	// Read latest (v1 GetRecord still works for backward compat)
	latest, err := svc.GetRecord(ctx, 42)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest.Data["hello"] != "world" || len(latest.Data) != 1 {
		t.Fatalf("unexpected latest data: %+v", latest)
	}

	// Asking for a version that doesn't exist on a real record
	if _, err := svc.GetRecordVersion(ctx, 42, 999); !errors.Is(err, ErrVersionDoesNotExist) {
		t.Fatalf("expected ErrVersionDoesNotExist for record 42 version 999, got %v", err)
	}

	// Asking for any version of a record that doesn't exist
	if _, err := svc.GetRecordVersion(ctx, 99999, 1); !errors.Is(err, ErrRecordDoesNotExist) {
		t.Fatalf("expected ErrRecordDoesNotExist for record 99999, got %v", err)
	}

	// Listing versions of a record that doesn't exist
	if _, err := svc.ListVersions(ctx, 99999); !errors.Is(err, ErrRecordDoesNotExist) {
		t.Fatalf("expected ErrRecordDoesNotExist for list of 99999, got %v", err)
	}
}

// Ensure SQLiteRecordService satisfies VersionedRecordService at compile time.
var _ VersionedRecordService = (*SQLiteRecordService)(nil)
