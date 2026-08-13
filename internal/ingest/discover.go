package ingest

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// defaultIgnoredDirs are skipped during file discovery: build artifacts,
// dependency caches, and VCS internals that would otherwise pollute the
// graph with vendored/generated code.
var defaultIgnoredDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, ".idea": true, ".vscode": true, "bin": true, "obj": true,
}

// SourceFile is a discovered file relevant to analysis.
type SourceFile struct {
	AbsPath string // absolute path on disk
	RelPath string // path relative to the repo root, forward-slash separated
	Ext     string // lowercase extension, including the leading dot
}

// DiscoverFiles walks root and returns every file whose extension is in
// extensions (map of ".go" -> true, etc.), skipping common
// build/dependency/VCS directories.
func DiscoverFiles(root string, extensions map[string]bool) ([]SourceFile, error) {
	var files []SourceFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if defaultIgnoredDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !extensions[ext] {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, SourceFile{
			AbsPath: p,
			RelPath: filepath.ToSlash(rel),
			Ext:     ext,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
