package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// File tools: read_file / write_file / edit_file.
//
// Design notes (deliberately conservative):
//   - Paths are resolved relative to the process working directory and must stay
//     inside it; absolute paths and ".." escapes are rejected.
//   - read_file is windowed (offset/limit) and line-numbered, so the model never
//     has to pull a whole large file into context.
//   - write_file never clobbers an existing file unless overwrite=true.
//   - edit_file anchors on an exact substring, not a line number. It refuses
//     ambiguous anchors (>1 match) unless replace_all=true, and it reports the
//     resulting hunk back so the caller can verify what changed. Failures are
//     loud; nothing is "repaired" silently.

const (
	defaultReadLimit = 200
	maxReadLines     = 2000
	maxLineChars     = 400
	maxDiffLines     = 60
)

// resolvePath validates a caller-supplied path and returns the absolute path.
func resolvePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path is required")
	}
	base, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %v", err)
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(base, abs)
	}
	abs = filepath.Clean(abs)

	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q is outside the working directory (%s)", p, base)
	}

	// If the path (or its parent, for new files) exists, make sure symlinks
	// cannot point outside the working directory.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if r, err := filepath.Rel(base, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("path %q resolves outside the working directory", p)
		}
	} else if resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		if r, err := filepath.Rel(base, resolvedDir); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("path %q resolves outside the working directory", p)
		}
	}
	return abs, nil
}

func readTextFile(p string) (string, error) {
	abs, err := resolvePath(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %v", p, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file", p)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %v", p, err)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not valid UTF-8 text (%d bytes)", p, len(data))
	}
	return string(data), nil
}

// atomicWrite writes data to abs via a temp file + rename so a crash cannot
// leave a half-written file behind.
func atomicWrite(abs, content string) error {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".x01-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func lineNumberAt(s string, idx int) int {
	if idx < 0 {
		return 0
	}
	return strings.Count(s[:idx], "\n") + 1
}

// ReadFile returns a numbered window of a UTF-8 text file.
func ReadFile(ctx context.Context, args map[string]interface{}) string {
	path, _ := args["path"].(string)

	offset := intArg(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intArg(args, "limit", defaultReadLimit)
	if limit < 1 {
		limit = defaultReadLimit
	}
	if limit > maxReadLines {
		limit = maxReadLines
	}

	content, err := readTextFile(path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	raw := strings.Split(content, "\n")
	// A trailing newline produces one empty trailing element that is not a line.
	if len(raw) > 0 && raw[len(raw)-1] == "" && strings.HasSuffix(content, "\n") {
		raw = raw[:len(raw)-1]
	}
	total := len(raw)

	if total == 0 {
		return fmt.Sprintf("%s (empty file, 0 lines, %d bytes)", path, len(content))
	}
	if offset > total {
		return fmt.Sprintf("Error: offset %d is past end of %s (%d lines)", offset, path, total)
	}

	end := offset - 1 + limit
	if end > total {
		end = total
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s (lines %d-%d of %d, %d bytes)\n", path, offset, end, total, len(content))
	for i := offset - 1; i < end; i++ {
		line := raw[i]
		if len(line) > maxLineChars {
			line = line[:maxLineChars] + fmt.Sprintf("... [+%d chars]", len(line)-maxLineChars)
		}
		fmt.Fprintf(&b, "%6d| %s\n", i+1, line)
	}
	if end < total {
		fmt.Fprintf(&b, "... [%d more lines; use offset=%d to continue]\n", total-end, end+1)
	}
	return b.String()
}

// WriteFile creates a new file, or replaces an existing one only when
// overwrite=true.
func WriteFile(ctx context.Context, args map[string]interface{}) string {
	path, _ := args["path"].(string)
	content, ok := args["content"].(string)
	if !ok {
		return "Error: content is required"
	}
	overwrite, _ := args["overwrite"].(bool)

	abs, err := resolvePath(path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	prev := ""
	if info, err := os.Stat(abs); err == nil {
		if info.IsDir() {
			return fmt.Sprintf("Error: %s is a directory, not a file", path)
		}
		if !overwrite {
			return fmt.Sprintf("Error: %s already exists (%d lines). Pass overwrite=true to replace it, or use edit_file for a targeted change.", path, countLines(mustRead(abs)))
		}
		prev = mustRead(abs)
	}

	if err := atomicWrite(abs, content); err != nil {
		return fmt.Sprintf("Error: cannot write %s: %v", path, err)
	}

	msg := fmt.Sprintf("Wrote %s (%d lines, %d bytes)", path, countLines(content), len(content))
	if prev != "" {
		msg += fmt.Sprintf("; replaced previous content (%d lines -> %d lines)", countLines(prev), countLines(content))
	}
	return msg
}

func mustRead(abs string) string {
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return string(data)
}

// EditFile replaces an exact substring. The anchor must be unique unless
// replace_all=true.
func EditFile(ctx context.Context, args map[string]interface{}) string {
	path, _ := args["path"].(string)
	oldStr, ok := args["old_string"].(string)
	if !ok || oldStr == "" {
		return "Error: old_string is required and must not be empty"
	}
	newStr, _ := args["new_string"].(string)
	replaceAll, _ := args["replace_all"].(bool)
	dryRun, _ := args["dry_run"].(bool)

	if oldStr == newStr {
		return "Error: old_string and new_string are identical; nothing to do"
	}

	content, err := readTextFile(path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	count := strings.Count(content, oldStr)
	if count == 0 {
		return fmt.Sprintf("Error: old_string not found in %s (0 matches). Read the file first and use the exact current text, including indentation.", path)
	}

	if count > 1 && !replaceAll {
		return fmt.Sprintf("Error: old_string matched %d times in %s (lines %s). Add surrounding context to make it unique, or pass replace_all=true to change all %d.",
			count, path, formatLineList(matchLineNumbers(content, oldStr)), count)
	}

	updated := strings.Replace(content, oldStr, newStr, 1)
	if replaceAll {
		updated = strings.ReplaceAll(content, oldStr, newStr)
	}

	changedLines := count
	if !replaceAll {
		changedLines = 1
	}

	if dryRun {
		return fmt.Sprintf("Dry run: would change %d occurrence(s) in %s\n%s", changedLines, path, diffHunk(content, oldStr, newStr, replaceAll))
	}

	if err := atomicWrite(mustResolve(path), updated); err != nil {
		return fmt.Sprintf("Error: cannot write %s: %v", path, err)
	}

	delta := countLines(updated) - countLines(content)
	return fmt.Sprintf("Edited %s (%d change(s), %+d lines)\n%s", path, changedLines, delta,
		diffHunk(content, oldStr, newStr, replaceAll))
}

func mustResolve(p string) string {
	abs, err := resolvePath(p)
	if err != nil {
		return p
	}
	return abs
}

func matchLineNumbers(content, old string) []int {
	var lines []int
	for i := 0; i+len(old) <= len(content); {
		idx := strings.Index(content[i:], old)
		if idx < 0 {
			break
		}
		abs := i + idx
		lines = append(lines, lineNumberAt(content, abs))
		i = abs + 1
	}
	return lines
}

func formatLineList(lines []int) string {
	sort.Ints(lines)
	parts := make([]string, 0, len(lines))
	for i, l := range lines {
		if i >= 10 {
			parts = append(parts, fmt.Sprintf("+%d more", len(lines)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%d", l))
	}
	return strings.Join(parts, ", ")
}

// diffHunk renders a compact, readable summary of the change.
func diffHunk(content, old, new string, all bool) string {
	first := strings.Index(content, old)
	if first < 0 {
		return ""
	}
	startLine := lineNumberAt(content, first)

	// Context: up to 2 lines before the anchor.
	lineStart := strings.LastIndex(content[:first], "\n") + 1
	ctxStart := lineStart
	for i := 0; i < 2; i++ {
		prev := strings.LastIndex(strings.TrimSuffix(content[:ctxStart], "\n"), "\n")
		if prev < 0 {
			ctxStart = 0
			break
		}
		ctxStart = prev + 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "@@ %s line %d @@\n", "anchor", startLine)
	for _, l := range splitKeep(content[ctxStart:lineStart]) {
		fmt.Fprintf(&b, "  %s\n", l)
	}
	for _, l := range splitKeep(old) {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	for _, l := range splitKeep(new) {
		fmt.Fprintf(&b, "+ %s\n", l)
	}
	if all {
		fmt.Fprintf(&b, "(all %d occurrences replaced)\n", strings.Count(content, old))
	}
	out := b.String()
	if lines := strings.Split(out, "\n"); len(lines) > maxDiffLines {
		out = strings.Join(lines[:maxDiffLines], "\n") + fmt.Sprintf("\n... [diff truncated at %d lines]\n", maxDiffLines)
	}
	return out
}

func splitKeep(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// intArg reads an integer argument tolerating JSON's float64 decoding.
func intArg(args map[string]interface{}, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return def
}
