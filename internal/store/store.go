// Package store reads and writes errors.sqlite3 with the same tables and
// value formats as auto_reset.py and ocr_samples.py, so either server can run on it.
package store

import (
	"database/sql"
	"encoding/json"

	_ "modernc.org/sqlite"
)

// DefaultCrop is DISPLAY_CROP in auto_reset.py: left, top, right, bottom.
var DefaultCrop = [4]float64{0.631, 0.585, 0.676, 0.691}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS errors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		ocr_text TEXT NOT NULL,
		raw_image BLOB NOT NULL,
		cropped_image BLOB NOT NULL,
		fixed_image BLOB NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ocr_sample_selection (sample_id TEXT PRIMARY KEY, selected_at REAL NOT NULL)`,
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) setting(key string) (string, bool) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) put(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, string(b))
	return err
}

func (s *Store) Crop() [4]float64 {
	v, ok := s.setting("display_crop")
	var c []float64
	if !ok || json.Unmarshal([]byte(v), &c) != nil || len(c) != 4 {
		return DefaultCrop
	}
	return [4]float64(c)
}

func (s *Store) SaveCrop(c [4]float64) error { return s.put("display_crop", c[:]) }

// View is nil when none was saved (the template renders it as null).
func (s *Store) View() map[string]float64 {
	v, ok := s.setting("display_view")
	var m map[string]float64
	if !ok || json.Unmarshal([]byte(v), &m) != nil {
		return nil
	}
	return m
}

func (s *Store) SaveView(v map[string]float64) error { return s.put("display_view", v) }

func (s *Store) Selection() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT sample_id FROM ocr_sample_selection`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) UpdateSelection(sel, desel []string, now float64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range sel {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO ocr_sample_selection (sample_id, selected_at) VALUES (?, ?)`, id, now); err != nil {
			return err
		}
	}
	for _, id := range desel {
		if _, err := tx.Exec(`DELETE FROM ocr_sample_selection WHERE sample_id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
