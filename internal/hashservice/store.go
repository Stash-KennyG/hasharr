package hashservice

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Profile struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Enabled      bool      `json:"enabled"`
	RemotePath   string    `json:"remotePath"`
	HasharrPath  string    `json:"hasharrPath"`
	StashIndex   int       `json:"stashIndex"`
	MaxTimeDelta float64   `json:"maxTimeDelta"`
	MaxDistance  int       `json:"maxDistance"`
	ApplyActions bool      `json:"applyActions"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("hash service db path is required")
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

func (s *Store) List(ctx context.Context) ([]Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,enabled,remote_path,hasharr_path,stash_index,max_time_delta,max_distance,apply_actions,created_at,updated_at FROM hash_service_profiles ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		var enabled, apply int
		var createdAtRaw, updatedAtRaw string
		if err := rows.Scan(&p.ID, &p.Name, &enabled, &p.RemotePath, &p.HasharrPath, &p.StashIndex, &p.MaxTimeDelta, &p.MaxDistance, &apply, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		p.ApplyActions = apply != 0
		p.CreatedAt = parseSQLiteTime(createdAtRaw)
		p.UpdatedAt = parseSQLiteTime(updatedAtRaw)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (Profile, error) {
	var p Profile
	var enabled, apply int
	var createdAtRaw, updatedAtRaw string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,enabled,remote_path,hasharr_path,stash_index,max_time_delta,max_distance,apply_actions,created_at,updated_at FROM hash_service_profiles WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &enabled, &p.RemotePath, &p.HasharrPath, &p.StashIndex, &p.MaxTimeDelta, &p.MaxDistance, &apply, &createdAtRaw, &updatedAtRaw)
	if err != nil {
		return Profile{}, err
	}
	p.Enabled = enabled != 0
	p.ApplyActions = apply != 0
	p.CreatedAt = parseSQLiteTime(createdAtRaw)
	p.UpdatedAt = parseSQLiteTime(updatedAtRaw)
	return p, nil
}

func (s *Store) Upsert(ctx context.Context, p Profile) (Profile, error) {
	if p.Name == "" {
		return Profile{}, fmt.Errorf("name is required")
	}
	if p.MaxTimeDelta < 0 {
		p.MaxTimeDelta = 0
	}
	if p.MaxTimeDelta > 15 {
		p.MaxTimeDelta = 15
	}
	if p.MaxDistance < 0 {
		p.MaxDistance = 0
	}
	if p.MaxDistance > 8 {
		p.MaxDistance = 8
	}
	if p.ID <= 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO hash_service_profiles(name,enabled,remote_path,hasharr_path,stash_index,max_time_delta,max_distance,apply_actions) VALUES(?,?,?,?,?,?,?,?)`,
			p.Name, boolInt(p.Enabled), p.RemotePath, p.HasharrPath, p.StashIndex, p.MaxTimeDelta, p.MaxDistance, boolInt(p.ApplyActions))
		if err != nil {
			return Profile{}, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return Profile{}, err
		}
		return s.Get(ctx, id)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE hash_service_profiles SET name=?,enabled=?,remote_path=?,hasharr_path=?,stash_index=?,max_time_delta=?,max_distance=?,apply_actions=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, boolInt(p.Enabled), p.RemotePath, p.HasharrPath, p.StashIndex, p.MaxTimeDelta, p.MaxDistance, boolInt(p.ApplyActions), p.ID)
	if err != nil {
		return Profile{}, err
	}
	return s.Get(ctx, p.ID)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM hash_service_profiles WHERE id=?`, id)
	return err
}

func ensureSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
version INTEGER PRIMARY KEY,
applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version < 1 {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS hash_service_profiles (
id INTEGER PRIMARY KEY AUTOINCREMENT,
name TEXT NOT NULL,
enabled INTEGER NOT NULL DEFAULT 1,
stash_index INTEGER NOT NULL DEFAULT -1,
max_time_delta REAL NOT NULL DEFAULT 1,
max_distance INTEGER NOT NULL DEFAULT 0,
apply_actions INTEGER NOT NULL DEFAULT 1,
remote_path TEXT NOT NULL DEFAULT '',
hasharr_path TEXT NOT NULL DEFAULT '',
created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO hash_service_profiles(id,name,enabled,stash_index,max_time_delta,max_distance,apply_actions)
VALUES(1,'metube-default',1,-1,1,0,1),
      (2,'sab-default',1,-1,1,0,1)`); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(1,CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err := db.Exec(`ALTER TABLE hash_service_profiles ADD COLUMN remote_path TEXT NOT NULL DEFAULT ''`); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				return err
			}
		}
		if _, err := db.Exec(`ALTER TABLE hash_service_profiles ADD COLUMN hasharr_path TEXT NOT NULL DEFAULT ''`); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				return err
			}
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(2,CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func parseSQLiteTime(raw string) time.Time {
	s := raw
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}
