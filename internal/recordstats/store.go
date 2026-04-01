package recordstats

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Record struct {
	SABNzoID            string
	FileName            string
	FileSizeBytes       int64
	FileDurationSeconds float64
	HashDurationSeconds float64
	Outcome             int
}

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("stats db path is required")
	}
	dsn := filepath.Clean(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := ensureSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Insert(ctx context.Context, r Record) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("stats store is not initialized")
	}
	if r.FileName == "" {
		return fmt.Errorf("fileName is required")
	}
	if r.Outcome < 0 || r.Outcome > 15 {
		return fmt.Errorf("outcome must be between 0 and 15")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO record_stats (
			sab_nzo_id,
			file_name,
			file_size_bytes,
			file_duration_seconds,
			hash_duration_seconds,
			outcome,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.SABNzoID,
		r.FileName,
		r.FileSizeBytes,
		r.FileDurationSeconds,
		r.HashDurationSeconds,
		r.Outcome,
		now,
	)
	return err
}

func ensureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS record_stats (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sab_nzo_id TEXT NOT NULL,
	file_name TEXT NOT NULL,
	file_size_bytes INTEGER NOT NULL,
	file_duration_seconds REAL NOT NULL,
	hash_duration_seconds REAL NOT NULL,
	outcome INTEGER NOT NULL,
	created_at TEXT NOT NULL
)`)
	return err
}
