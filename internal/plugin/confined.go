package plugin

import (
	"net/http"
	"os"
	"path/filepath"
)

// A running plugin can create symlinks in its own directory. The fs module keeps
// the plugin itself confined, but the server reads the same tree, so its reads
// have to refuse links that leave the directory as well. os.Root does that at
// open time, which leaves no gap between a check and the read.

func readConfined(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(name)
}

// serveConfined serves the file at path, which must lie under dir, and answers
// 404 for anything that is not a regular file reached without leaving dir.
func serveConfined(w http.ResponseWriter, r *http.Request, dir, path string) {
	relative, err := filepath.Rel(dir, path)
	if err != nil || !filepath.IsLocal(relative) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// ServeInstalledFile serves a path returned by ResolveFile or ResolvePublicFile.
func ServeInstalledFile(w http.ResponseWriter, r *http.Request, short, path string) {
	serveConfined(w, r, filepath.Join(DataDir, short), path)
}
