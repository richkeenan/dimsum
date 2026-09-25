package clients

import (
	_ "embed"
	"slices"
	"strings"
)

//go:embed builtin/lucide-icons.txt
var lucideIconNames string

var lucideIcons = func() map[string]bool {
	names := make(map[string]bool)
	for _, line := range strings.Split(lucideIconNames, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			names[line] = true
		}
	}
	return names
}()

// ValidIcon accepts an optional kebab-case name from the bundled Lucide version.
func ValidIcon(name string) bool { return name == "" || lucideIcons[name] }

// IconNames returns the installed names in alphabetical order. Search matches
// a case-insensitive substring; an empty search returns the complete catalogue.
func IconNames(search string) []string {
	search = strings.ToLower(strings.TrimSpace(search))
	names := make([]string, 0)
	for name := range lucideIcons {
		if strings.Contains(name, search) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
