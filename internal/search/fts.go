package search

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Conn is a SQLite FTS database handle.
type Conn struct {
	*sql.DB
}

// Open opens or creates SQLite FTS5 index at path (e.g. data/search/{space}.sqlite).
func Open(dbPath string) (*Conn, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	dsn := dbPath + "?_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(path UNINDEXED, title, body);
`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("fts init: %w", err)
	}
	return &Conn{DB: db}, nil
}

// UpsertPage indexes a markdown page (replaces row for path).
func UpsertPage(db *Conn, path, title, body string) error {
	_, _ = db.DB.Exec(`DELETE FROM pages_fts WHERE path = ?`, path)
	_, err := db.DB.Exec(`INSERT INTO pages_fts(path, title, body) VALUES(?,?,?)`, path, title, body)
	return err
}

// Hit is one search result.
type Hit struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Search runs FTS query.
func Search(db *Conn, q string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.DB.Query(`
SELECT path, title, snippet(pages_fts, 2, '<b>', '</b>', ' … ', 32) 
FROM pages_fts WHERE pages_fts MATCH ? ORDER BY rank LIMIT ?`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Path, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
