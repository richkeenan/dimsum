package clients

import (
	_ "embed"
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
