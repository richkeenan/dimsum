package lists

import (
	_ "embed"
)

// WorkCompatibilityURL identifies the independently maintained allowlist shipped
// with dimsum. Its contents change only when the executable is updated.
const WorkCompatibilityURL = "builtin://work-compatibility"

//go:embed builtin/work-compatibility.adblock
var workCompatibility string
