package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store persists a Graph to a local SQLite database (pure Go driver, no
// CGO). Each analyzed repository gets its own database file under
// ~/.graphsentry/<repo-hash>/graph.db.
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
	doc_comment TEXT,
	attrs       TEXT,
	wraps_type  TEXT NOT NULL DEFAULT '',
	implements  TEXT
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
	if err := s.migrateAddQualifiedColumn(); err != nil {
		return err
	}
	return s.migrateAddAttrColumns()
}

// migrateAddAttrColumns adds the "attrs"/"wraps_type"/"implements" columns
// to a nodes table created before they existed — same reasoning and same
// PRAGMA-table_info approach as migrateAddQualifiedColumn.
func (s *Store) migrateAddAttrColumns() error {
	existing, err := s.nodeColumns()
	if err != nil {
		return err
	}
	if !existing["attrs"] {
		if _, err := s.db.Exec(`ALTER TABLE nodes ADD COLUMN attrs TEXT`); err != nil {
			return err
		}
	}
	if !existing["wraps_type"] {
		if _, err := s.db.Exec(`ALTER TABLE nodes ADD COLUMN wraps_type TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !existing["implements"] {
		if _, err := s.db.Exec(`ALTER TABLE nodes ADD COLUMN implements TEXT`); err != nil {
			return err
		}
	}
	return nil
}

// nodeColumns returns the set of column names currently on the nodes table.
func (s *Store) nodeColumns() (map[string]bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(nodes)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// migrateAddQualifiedColumn adds the "qualified" column to a nodes table
// created before it existed. CREATE TABLE IF NOT EXISTS above is a no-op
// against an existing (pre-migration) database file, so without this a
// repo analyzed before this column was introduced would hit a column-count
// mismatch on the next `graphsentry analyze` instead of just picking up the
// new column.
func (s *Store) migrateAddQualifiedColumn() error {
	existing, err := s.nodeColumns()
	if err != nil {
		return err
	}
	if existing["qualified"] {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE nodes ADD COLUMN qualified TEXT NOT NULL DEFAULT ''`)
	return err
}

// Save truncates the nodes/edges tables and writes the full graph. Analysis
// is a full rebuild rather than an incremental diff, so this keeps the
// store consistent with the latest `graphsentry analyze` run.
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

	nodeStmt, err := tx.Prepare(`INSERT INTO nodes (id, kind, name, qualified, file, language, start_line, end_line, signature, doc_comment, attrs, wraps_type, implements) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = nodeStmt.Close() }()
	for _, n := range g.Nodes {
		// Attrs/Implements are stored as JSON — both are small,
		// variable-shaped lists that don't warrant their own table, the
		// same tradeoff the embeddings/churn tables don't need to make
		// here since they're always read back whole, never queried by
		// field.
		attrsJSON, err := json.Marshal(n.Attrs)
		if err != nil {
			return fmt.Errorf("marshal attrs for node %s: %w", n.ID, err)
		}
		implementsJSON, err := json.Marshal(n.Implements)
		if err != nil {
			return fmt.Errorf("marshal implements for node %s: %w", n.ID, err)
		}
		if _, err := nodeStmt.Exec(n.ID, string(n.Kind), n.Name, n.Qualified, n.File, n.Language, n.StartLine, n.EndLine, n.Signature, n.DocComment, string(attrsJSON), n.WrapsType, string(implementsJSON)); err != nil {
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

	rows, err := s.db.Query(`SELECT id, kind, name, qualified, file, language, start_line, end_line, signature, doc_comment, attrs, wraps_type, implements FROM nodes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		n := &Node{}
		var sig, doc, attrsJSON, implementsJSON sql.NullString
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.Qualified, &n.File, &n.Language, &n.StartLine, &n.EndLine, &sig, &doc, &attrsJSON, &n.WrapsType, &implementsJSON); err != nil {
			_ = rows.Close()
			return nil, err
		}
		n.Signature = sig.String
		n.DocComment = doc.String
		if attrsJSON.String != "" {
			if err := json.Unmarshal([]byte(attrsJSON.String), &n.Attrs); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("unmarshal attrs for node %s: %w", n.ID, err)
			}
		}
		if implementsJSON.String != "" {
			if err := json.Unmarshal([]byte(implementsJSON.String), &n.Implements); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("unmarshal implements for node %s: %w", n.ID, err)
			}
		}
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
