package hooks

import "testing"

func TestFormatToolDetail(t *testing.T) {
	cases := []struct {
		tool string
		in   map[string]interface{}
		want string
	}{
		{"Bash", map[string]interface{}{"command": "go test ./..."}, "go test ./..."},
		{"Edit", map[string]interface{}{"file_path": "a/b.go", "old_string": "x", "new_string": "y"}, "a/b.go\n- x\n+ y"},
		{"Write", map[string]interface{}{"file_path": "a/b.go", "content": "package main"}, "a/b.go\npackage main"},
		{"Grep", map[string]interface{}{"pattern": "foo.*bar", "path": "src"}, "pattern: foo.*bar\npath: src"},
		{"Read", map[string]interface{}{"file_path": "a/b.go"}, "a/b.go"},
		{"WebSearch", map[string]interface{}{"query": "golang tips"}, "golang tips"},
	}
	for _, c := range cases {
		if got := FormatToolDetail(c.tool, c.in); got != c.want {
			t.Errorf("FormatToolDetail(%q) = %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestFormatToolDetailNilInput(t *testing.T) {
	if got := FormatToolDetail("Bash", nil); got != "" {
		t.Errorf("nil input should yield empty detail, got %q", got)
	}
}
