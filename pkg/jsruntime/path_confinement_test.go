package jsruntime

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNodeFSRelativePathsCannotCrossAuthorizedRoots(t *testing.T) {
	baseDir, storageDir := pathConfinementRoots(t)
	runtime, err := New(`
		async function verify() {
			const fs = require("fs");
			const path = require("path");
			const baseDir = __dirname;
			function denied(action) {
				try { action(); }
				catch (error) {
					if (!String(error).includes("escapes BaseDir")) throw error;
					return;
				}
				throw new Error("relative path crossed an authorized root");
			}
			async function deniedAsync(action) {
				try { await action(); }
				catch (error) {
					if (!String(error).includes("escapes BaseDir")) throw error;
					return;
				}
				throw new Error("async relative path crossed an authorized root");
			}
			const relativeStorage = path.join("..", path.basename(__storageDir__));
			denied(() => fs.readFileSync(path.join(relativeStorage, "saved.txt"), "utf8"));
			denied(() => fs.writeFileSync(path.join(relativeStorage, "denied-sync.txt"), "x"));
			await deniedAsync(() => fs.promises.readFile(path.join(relativeStorage, "saved.txt"), "utf8"));
			await deniedAsync(() => fs.promises.writeFile(path.join(relativeStorage, "denied-async.txt"), "x"));

			// Explicit absolute storage paths remain part of the supported API.
			fs.mkdirSync(__storageDir__, { recursive: true });
			fs.writeFileSync(path.join(__storageDir__, "absolute.txt"), "absolute", "utf8");
			if (await fs.promises.readFile(path.join(__storageDir__, "absolute.txt"), "utf8") !== "absolute") {
				throw new Error("explicit absolute storage access failed");
			}
			process.chdir(__storageDir__);
			if (fs.readFileSync("saved.txt", "utf8") !== "storage") throw new Error("storage cwd read failed");
			const relativeBase = path.join("..", path.basename(baseDir), "saved.txt");
			denied(() => fs.readFileSync(relativeBase, "utf8"));
			await deniedAsync(() => new Promise((resolve, reject) => {
				fs.readFile(relativeBase, "utf8", (error, value) => error ? reject(error) : resolve(value));
			}));
			if (fs.readFileSync(path.join(baseDir, "saved.txt"), "utf8") !== "base") {
				throw new Error("explicit absolute base access failed");
			}
			return true;
		}
	`, Options{NodeJS: true, BaseDir: baseDir, StorageDir: storageDir, Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"denied-sync.txt", "denied-async.txt"} {
		if _, err := os.Stat(filepath.Join(storageDir, name)); !os.IsNotExist(err) {
			t.Fatalf("cross-root operation created %s: %v", name, err)
		}
	}
}

func TestNodeFSSymlinksCannotSwitchAuthorizedRoots(t *testing.T) {
	baseDir, storageDir := pathConfinementRoots(t)
	if err := os.Mkdir(filepath.Join(baseDir, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "inner", "local.txt"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ target, name string }{
		{storageDir, filepath.Join(baseDir, "cross-storage")},
		{baseDir, filepath.Join(storageDir, "cross-base")},
		{"inner", filepath.Join(baseDir, "within-base")},
	} {
		if err := os.Symlink(link.target, link.name); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
	}
	runtime, err := New(`
		async function verify() {
			const fs = require("fs");
			const path = require("path");
			function denied(action) {
				try { action(); }
				catch (error) {
					if (!String(error).includes("escapes BaseDir")) throw error;
					return;
				}
				throw new Error("symlink switched an authorized root");
			}
			async function deniedAsync(action) {
				try { await action(); }
				catch (error) {
					if (!String(error).includes("escapes BaseDir")) throw error;
					return;
				}
				throw new Error("async symlink switched an authorized root");
			}
			denied(() => fs.readFileSync("cross-storage/saved.txt", "utf8"));
			denied(() => fs.writeFileSync("cross-storage/denied-sync.txt", "x"));
			await deniedAsync(() => fs.promises.readFile("cross-storage/saved.txt", "utf8"));
			await deniedAsync(() => fs.promises.writeFile("cross-storage/missing/denied-async.txt", "x"));
			denied(() => fs.readFileSync(path.join(__storageDir__, "cross-base", "saved.txt"), "utf8"));
			await deniedAsync(() => fs.promises.readFile(path.join(__storageDir__, "cross-base", "saved.txt"), "utf8"));
			if (fs.readFileSync("within-base/local.txt", "utf8") !== "local") {
				throw new Error("symlink within the original root was rejected");
			}
			if (!fs.lstatSync("cross-storage").isSymbolicLink()) throw new Error("lstat followed final symlink");
			await fs.promises.unlink("cross-storage");
			if (fs.readFileSync(path.join(__storageDir__, "saved.txt"), "utf8") !== "storage") {
				throw new Error("unlink changed the symlink target");
			}
			return true;
		}
	`, Options{NodeJS: true, BaseDir: baseDir, StorageDir: storageDir, Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Call("verify"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"denied-sync.txt", "missing"} {
		if _, err := os.Stat(filepath.Join(storageDir, name)); !os.IsNotExist(err) {
			t.Fatalf("symlink operation created %s: %v", name, err)
		}
	}
}

func pathConfinementRoots(t *testing.T) (baseDir, storageDir string) {
	t.Helper()
	root := t.TempDir()
	baseDir, storageDir = filepath.Join(root, "base"), filepath.Join(root, "storage")
	for _, entry := range []struct{ path, content string }{{baseDir, "base"}, {storageDir, "storage"}} {
		if err := os.Mkdir(entry.path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entry.path, "saved.txt"), []byte(entry.content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return baseDir, storageDir
}
