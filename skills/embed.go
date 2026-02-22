package skills

import "embed"

// FS embeds all SKILL.md files from skill subdirectories.
//
//go:embed */SKILL.md
var FS embed.FS
