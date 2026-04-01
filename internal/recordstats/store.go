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

type Summary struct {
	HashCount           int64   `json:"hashCount"`
	DataBytesSum        int64   `json:"dataBytesSum"`
	DeleteCount         int64   `json:"deleteCount"`
	LCount              int64   `json:"lCount"`
	FCount              int64   `json:"fCount"`
	DCount              int64   `json:"dCount"`
	VideoDurationSumSec float64 `json:"videoDurationSumSec"`
	HashDurationSumSec  float64 `json:"hashDurationSumSec"`
	Since               string  `json:"since"`
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

func (s *Store) Summary(ctx context.Context) (Summary, error) {
	if s == nil || s.db == nil {
		return Summary{}, fmt.Errorf("stats store is not initialized")
	}
	var out Summary
	row := s.db.QueryRowContext(
		ctx,
		`SELECT
			COALESCE(COUNT(*), 0) AS hash_count,
			COALESCE(SUM(file_size_bytes), 0) AS data_bytes_sum,
			COALESCE(SUM(CASE WHEN (outcome & 8) != 0 THEN 1 ELSE 0 END), 0) AS delete_count,
			COALESCE(SUM(CASE WHEN (outcome & 4) != 0 THEN 1 ELSE 0 END), 0) AS l_count,
			COALESCE(SUM(CASE WHEN (outcome & 1) != 0 THEN 1 ELSE 0 END), 0) AS f_count,
			COALESCE(SUM(CASE WHEN (outcome & 2) != 0 THEN 1 ELSE 0 END), 0) AS d_count,
			COALESCE(SUM(file_duration_seconds), 0) AS video_duration_sum_sec,
			COALESCE(SUM(hash_duration_seconds), 0) AS hash_duration_sum_sec,
			COALESCE(MIN(created_at), '') AS since
		FROM record_stats`,
	)
	if err := row.Scan(
		&out.HashCount,
		&out.DataBytesSum,
		&out.DeleteCount,
		&out.LCount,
		&out.FCount,
		&out.DCount,
		&out.VideoDurationSumSec,
		&out.HashDurationSumSec,
		&out.Since,
	); err != nil {
		return Summary{}, err
	}
	return out, nil
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
