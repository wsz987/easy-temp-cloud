package app

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const metadataSchema = `
CREATE TABLE IF NOT EXISTS files (
	id TEXT PRIMARY KEY,
	object_key TEXT NOT NULL,
	filename TEXT NOT NULL,
	content_type TEXT NOT NULL,
	size INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS files_expires_at_idx ON files(expires_at);`

type metadataStore struct {
	db *sql.DB
}

func openMetadata(path string) (*metadataStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open metadata database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(metadataSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize metadata database: %w", err)
	}
	return &metadataStore{db: db}, nil
}

func (m *metadataStore) Close() error {
	return m.db.Close()
}

func (m *metadataStore) insert(item record) error {
	_, err := m.db.Exec(`
		INSERT INTO files (id, object_key, filename, content_type, size, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.ObjectKey, item.Filename, item.ContentType, item.Size, item.Created.UnixNano(), item.Expires.UnixNano())
	if err != nil {
		return fmt.Errorf("insert metadata for %s: %w", item.ID, err)
	}
	return nil
}

func (m *metadataStore) delete(id string) error {
	if _, err := m.db.Exec(`DELETE FROM files WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete metadata for %s: %w", id, err)
	}
	return nil
}

func (m *metadataStore) all() ([]record, error) {
	return m.query(`SELECT id, object_key, filename, content_type, size, created_at, expires_at FROM files ORDER BY id`)
}

func (m *metadataStore) expiredBefore(cutoff time.Time, limit int) ([]record, error) {
	return m.query(`
		SELECT id, object_key, filename, content_type, size, created_at, expires_at
		FROM files
		WHERE expires_at <= ?
		ORDER BY expires_at, id
		LIMIT ?
	`, cutoff.UnixNano(), limit)
}

func (m *metadataStore) query(statement string, args ...any) ([]record, error) {
	rows, err := m.db.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []record
	for rows.Next() {
		var item record
		var created, expires int64
		if err := rows.Scan(&item.ID, &item.ObjectKey, &item.Filename, &item.ContentType, &item.Size, &created, &expires); err != nil {
			return nil, err
		}
		item.Created = time.Unix(0, created).UTC()
		item.Expires = time.Unix(0, expires).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
