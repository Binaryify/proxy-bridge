package apps

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestList(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"Zed.app", "code.app", "Adobe/Acrobat.app", ".Hidden.app", "Broken.app"} {
		os.MkdirAll(filepath.Join(dir, p, "Contents"), 0o755)
		if p != "Broken.app" {
			os.WriteFile(filepath.Join(dir, p, "Contents", "Info.plist"), nil, 0o644)
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)

	var names []string
	for _, a := range List(dir, filepath.Join(dir, "missing")) {
		names = append(names, a.Name)
	}
	// Sorted without regard to case, one folder deep, apps only.
	if want := []string{"Acrobat", "code", "Zed"}; !slices.Equal(names, want) {
		t.Errorf("List = %q, want %q", names, want)
	}
}
