package graph

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store persists a Graph to a local SQLite database (pure Go driver, no
// CGO). Each analyzed repository gets its own database file under
// ~/.repolens/<repo-hash>/graph.db.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if needed) the SQLite database at path and
// ensures the schema exists.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS nodes (
	id          TEXT PRIMARY KEY,
	kind        TEXT NOT NULL,
	name        TEXT NOT NULL,
	qualified   TEXT NOT NULL DEFAULT '',
	file        TEXT NOT NULL,
	language    TEXT NOT NULL,
	start_line  INTEGER NOT NULL,
	end_line    INTEGER NOT NULL,
	signature   TEXT,
	doc_comment TEXT
);
CREATE TABLE IF NOT EXISTS edges (
	from_id TEXT NOT NULL,
	to_id   TEXT NOT NULL,
	kind    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_id);
CREATE INDEX IF NOT EXISTS idx_nodes_file ON nodes(file);

CREATE TABLE IF NOT EXISTS embeddings (
	node_id TEXT PRIMARY KEY,
	model   TEXT NOT NULL,
	vector  BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS churn (
	file        TEXT PRIMARY KEY,
	commits     INTEGER NOT NULL,
	last_change TEXT
);

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	return s.migrateAddQualifiedColumn()
}

// migrateAddQualifiedColumn adds the "qualified" column to a nodes table
// created before it existed. CREATE TABLE IF NOT EXISTS above is a no-op
// against an existing (pre-migration) database file, so without this a
// repo analyzed before this column was introduced would hit a column-count
// mismatch on the next `repolens analyze` instead of just picking up the
// new column.
func (s *Store) migrateAddQualifiedColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(nodes)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	hasQualified := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "qualified" {
			hasQualified = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasQualified {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE nodes ADD COLUMN qualified TEXT NOT NULL DEFAULT ''`)
	return err
}

// Save truncates the nodes/edges tables and writes the full graph. Analysis
// is a full rebuild rather than an incremental diff, so this keeps the
// store consistent with the latest `repolens analyze` run.
func (s *Store) Save(g *Graph) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds

	if _, err := tx.Exec(`DELETE FROM nodes`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM edges`); err != nil {
		return err
	}

	nodeStmt, err := tx.Prepare(`INSERT INTO nodes (id, kind, name, qualified, file, language, start_line, end_line, signature, doc_comment) VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = nodeStmt.Close() }()
	for _, n := range g.Nodes {
		if _, err := nodeStmt.Exec(n.ID, string(n.Kind), n.Name, n.Qualified, n.File, n.Language, n.StartLine, n.EndLine, n.Signature, n.DocComment); err != nil {
			return fmt.Errorf("insert node %s: %w", n.ID, err)
		}
	}

	edgeStmt, err := tx.Prepare(`INSERT INTO edges (from_id, to_id, kind) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = edgeStmt.Close() }()
	for _, e := range g.Edges {
		if _, err := edgeStmt.Exec(e.From, e.To, string(e.Kind)); err != nil {
			return fmt.Errorf("insert edge %s->%s: %w", e.From, e.To, err)
		}
	}

	return tx.Commit()
}

// Load reads the full graph back from the database.
func (s *Store) Load() (*Graph, error) {
	g := New()

	rows, err := s.db.Query(`SELECT id, kind, name, qualified, file, language, start_line, end_line, signature, doc_comment FROM nodes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		n := &Node{}
		var sig, doc sql.NullString
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.Qualified, &n.File, &n.Language, &n.StartLine, &n.EndLine, &sig, &doc); err != nil {
			_ = rows.Close()
			return nil, err
		}
		n.Signature = sig.String
		n.DocComment = doc.String
		g.Nodes[n.ID] = n
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	erows, err := s.db.Query(`SELECT from_id, to_id, kind FROM edges`)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		e := &Edge{}
		if err := erows.Scan(&e.From, &e.To, &e.Kind); err != nil {
			_ = erows.Close()
			return nil, err
		}
		g.Edges = append(g.Edges, e)
	}
	_ = erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}

	g.Reindex()
	return g, nil
}

// SetMeta stores a key/value pair (e.g. repo URL, last analyzed commit).
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// GetMeta retrieves a value previously stored with SetMeta.
func (s *Store) GetMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
