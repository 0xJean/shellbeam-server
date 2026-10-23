package public

import (
	"fmt"
	"io/fs"
)

// PrepareDefaultDist preserves the startup API while validating the compiled-in
// connection page. Assets are served directly from PublicFS; no archive, cache,
// writable directory, frontend toolchain, or network access is needed.
func PrepareDefaultDist() error {
	info, err := fs.Stat(PublicFS, "defaultTheme/dist/"+IndexFile)
	if err != nil {
		return fmt.Errorf("read embedded ShellBeam connection page: %w", err)
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("embedded ShellBeam connection page is empty")
	}
	return nil
}
