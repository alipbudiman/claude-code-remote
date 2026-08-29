package hooks

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxDisplayLength = 60

// FormatToolStatus translates raw Claude Code tool names and inputs into human-friendly descriptions
func FormatToolStatus(toolName string, input map[string]interface{}) string {
	if input == nil {
		input = make(map[string]interface{})
	}

	getString := func(key string) string {
		if val, ok := input[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
		return ""
	}

	baseName := func(p string) string {
		if p == "" {
			return ""
		}
		return filepath.Base(p)
	}

	truncate := func(s string, limit int) string {
		s = strings.TrimSpace(s)
		if len(s) > limit {
			return s[:limit] + "…"
		}
		return s
	}

	switch toolName {
	case "Read", "ReadFile":
		fp := getString("file_path")
		if fp == "" {
			fp = getString("path")
		}
		if fp != "" {
			return fmt.Sprintf("Reading %s", baseName(fp))
		}
		return "Reading file"

	case "Edit", "EditFile", "replace_file_content", "multi_replace_file_content":
		fp := getString("file_path")
		if fp == "" {
			fp = getString("path")
		}
		if fp != "" {
			return fmt.Sprintf("Editing %s", baseName(fp))
		}
		return "Editing code"

	case "Write", "WriteFile", "write_to_file":
		fp := getString("file_path")
		if fp == "" {
			fp = getString("path")
		}
		if fp != "" {
			return fmt.Sprintf("Writing %s", baseName(fp))
		}
		return "Writing file"

	case "Bash", "run_command", "Terminal":
		cmd := getString("command")
		if cmd == "" {
			cmd = getString("CommandLine")
		}
		if cmd != "" {
			return fmt.Sprintf("Running: %s", truncate(cmd, maxDisplayLength))
		}
		return "Running terminal command"

	case "Glob":
		pattern := getString("pattern")
		if pattern != "" {
			return fmt.Sprintf("Finding files: %s", pattern)
		}
		return "Searching files"

	case "Grep", "grep_search":
		query := getString("query")
		if query == "" {
			query = getString("Query")
		}
		if query != "" {
			return fmt.Sprintf("Searching code: %s", truncate(query, 30))
		}
		return "Searching code"

	case "WebFetch", "read_url_content":
		url := getString("url")
		if url == "" {
			url = getString("Url")
		}
		if url != "" {
			return fmt.Sprintf("Fetching %s", truncate(url, 40))
		}
		return "Fetching web content"

	case "WebSearch", "search_web":
		query := getString("query")
		if query != "" {
			return fmt.Sprintf("Searching web: %s", truncate(query, 35))
		}
		return "Searching the web"

	case "Task", "Agent", "invoke_subagent":
		desc := getString("description")
		if desc == "" {
			desc = getString("Task")
		}
		if desc != "" {
			return fmt.Sprintf("Subtask: %s", truncate(desc, maxDisplayLength))
		}
		return "Spawning sub-agent"

	case "AskUserQuestion", "ask_question":
		return "Waiting for user input"

	case "EnterPlanMode", "enter_plan_mode":
		return "Planning task steps"

	case "ExitPlanMode", "exit_plan_mode":
		return "Plan ready, waiting for review (ExitPlanMode)"

	case "NotebookEdit":
		return "Editing notebook"

	case "TeamCreate":
		team := getString("team_name")
		if team != "" {
			return fmt.Sprintf("Creating team: %s", team)
		}
		return "Creating agent team"

	case "SendMessage":
		recipient := getString("recipient")
		if recipient != "" {
			return fmt.Sprintf("Messaging -> %s", recipient)
		}
		return "Sending message"

	default:
		if toolName == "" {
			return "Working on task"
		}
		return fmt.Sprintf("Using %s", toolName)
	}
}
