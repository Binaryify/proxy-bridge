// Package apps lists the apps installed on this Mac.
package apps

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// App is an application bundle.
type App struct {
	Path string
	Name string
}

// Dirs are the folders apps are installed in.
func Dirs() []string {
	dirs := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

// List finds the apps in dirs, and in the folders within them such as
// "Adobe Acrobat DC", sorted by name.
func List(dirs ...string) []App {
	var apps []App
	add := func(path string) {
		if _, err := os.Stat(filepath.Join(path, "Contents", "Info.plist")); err == nil {
			apps = append(apps, App{Path: path, Name: strings.TrimSuffix(filepath.Base(path), ".app")})
		}
	}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			switch {
			case strings.HasPrefix(e.Name(), "."):
			case strings.HasSuffix(e.Name(), ".app"):
				add(path)
			case e.IsDir():
				inner, _ := os.ReadDir(path)
				for _, ie := range inner {
					if strings.HasSuffix(ie.Name(), ".app") {
						add(filepath.Join(path, ie.Name()))
					}
				}
			}
		}
	}
	slices.SortFunc(apps, func(a, b App) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Path, b.Path))
	})
	return apps
}
