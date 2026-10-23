package jsruntime

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFileWriteEncodingDoesNotBecomeFileMode(t *testing.T) {
	baseDir := t.TempDir()
	rt, err := New(`
		async function verify() {
			const fs = require("fs");
			fs.writeFileSync("sync.txt", "sync", "utf8");
			fs.appendFileSync("append.txt", "first", "utf8");
			fs.appendFileSync("append.txt", " second", "utf8");
			await fs.promises.writeFile("async.txt", "async", "utf8");
			await fs.promises.appendFile("async-append.txt", "async append", "utf8");
			await new Promise((resolve, reject) => fs.writeFile("callback.txt", "callback", "utf8", error => error ? reject(error) : resolve()));
			await new Promise((resolve, reject) => fs.appendFile("callback-append.txt", "callback append", { encoding: "utf8" }, error => error ? reject(error) : resolve()));
			fs.writeFileSync("encoding-object.txt", "encoding object", { encoding: "utf8" });
			fs.writeFileSync("private.txt", "private", { mode: 0o600 });
			await fs.promises.writeFile("zero.txt", "zero", { mode: 0 });
			for (const [name, expected] of [
				["sync.txt", "sync"], ["append.txt", "first second"],
				["async.txt", "async"], ["async-append.txt", "async append"],
				["callback.txt", "callback"], ["callback-append.txt", "callback append"],
				["encoding-object.txt", "encoding object"]
			]) {
				if (await fs.promises.readFile(name, "utf8") !== expected) {
					throw new Error("write encoding readback failed");
				}
			}
			return true;
		}
	`, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if err := rt.Call("verify"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return // Windows does not implement POSIX file permission bits.
	}
	for _, name := range []string{"sync.txt", "append.txt", "async.txt", "async-append.txt", "callback.txt", "callback-append.txt", "encoding-object.txt"} {
		info, err := os.Stat(filepath.Join(baseDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o600 != 0o600 {
			t.Fatalf("encoding option produced unreadable mode for %s: %v", name, info.Mode())
		}
	}
	for name, want := range map[string]os.FileMode{"private.txt": 0o600, "zero.txt": 0} {
		info, err := os.Stat(filepath.Join(baseDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("explicit mode for %s = %v, want %v", name, info.Mode().Perm(), want)
		}
	}
}
