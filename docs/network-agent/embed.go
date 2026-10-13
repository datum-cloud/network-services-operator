// SPDX-License-Identifier: AGPL-3.0-only

// Package networkagentdocs embeds the knowledge and skills networking publishes
// to an AI assistant about networks and network services.
package networkagentdocs

import "embed"

//go:embed llms-full.txt skills/*.md
var FS embed.FS

const (
	KnowledgeFile = "llms-full.txt"
	SkillsDir     = "skills"
)
