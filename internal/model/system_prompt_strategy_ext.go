package model

// StrategyInstructionsFile writes a VS Code .instructions.md file with YAML frontmatter.
const StrategyInstructionsFile SystemPromptStrategy = StrategyAppendToFile + 1

// StrategyJinjaModules writes separate module files that are included into a
// thin Jinja2 template (e.g. Kimi's KIMI.md). Each component writes its own
// file; persona inject also writes the static KIMI.md template.
const StrategyJinjaModules SystemPromptStrategy = StrategyInstructionsFile + 1
