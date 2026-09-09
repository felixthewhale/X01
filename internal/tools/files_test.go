package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirTemp moves the process into a throwaway directory and returns it. The
// file tools are confined to <dir>/sandbox, which sandboxRoot() creates lazily.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
	return dir
}

// sandboxFile is the real on-disk location of a path handed to the tools.
func sandboxFile(dir, rel string) string {
	return filepath.Join(dir, sandboxRootName, rel)
}

func readSandbox(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(sandboxFile(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteFileCreatesAndRefusesOverwrite(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()

	got := WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "hello\n"})
	if !strings.HasPrefix(got, "Wrote a.txt") {
		t.Fatalf("unexpected write result: %q", got)
	}
	if readSandbox(t, dir, "a.txt") != "hello\n" {
		t.Fatalf("content = %q", readSandbox(t, dir, "a.txt"))
	}

	got = WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "clobber\n"})
	if !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "already exists") {
		t.Fatalf("expected refusal, got %q", got)
	}
	if readSandbox(t, dir, "a.txt") != "hello\n" {
		t.Fatalf("file was clobbered: %q", readSandbox(t, dir, "a.txt"))
	}

	got = WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "clobber\n", "overwrite": true})
	if !strings.HasPrefix(got, "Wrote a.txt") {
		t.Fatalf("overwrite failed: %q", got)
	}
	if readSandbox(t, dir, "a.txt") != "clobber\n" {
		t.Fatalf("content = %q", readSandbox(t, dir, "a.txt"))
	}
}

func TestReadFileWindowAndNumbering(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "one\ntwo\nthree\nfour\n"})

	got := ReadFile(ctx, map[string]interface{}{"path": "a.txt", "offset": 2, "limit": 2})
	if !strings.Contains(got, "lines 2-3 of 4") {
		t.Fatalf("bad header: %q", got)
	}
	if !strings.Contains(got, "     2| two") || !strings.Contains(got, "     3| three") {
		t.Fatalf("bad body: %q", got)
	}
	if strings.Contains(got, "four") {
		t.Fatalf("window leaked past limit: %q", got)
	}
	if !strings.Contains(got, "1 more line") {
		t.Fatalf("missing continuation hint: %q", got)
	}
	_ = dir
}

func TestToolsAreConfinedToSandbox(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()

	// Escape attempts and absolute paths are refused.
	for _, p := range []string{"../outside.txt", "../../etc/passwd", "/etc/passwd", "C:\\Windows\\win.ini"} {
		got := ReadFile(ctx, map[string]interface{}{"path": p})
		if !strings.HasPrefix(got, "Error:") {
			t.Fatalf("expected refusal for %q, got %q", p, got)
		}
	}

	// A path that exists outside the workspace must stay invisible.
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("host secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := ReadFile(ctx, map[string]interface{}{"path": "secret.txt"})
	if !strings.Contains(got, "cannot read") {
		t.Fatalf("file outside the sandbox was readable: %q", got)
	}

	// Writes land inside <cwd>/sandbox, never next to it.
	WriteFile(ctx, map[string]interface{}{"path": "sub/inner.txt", "content": "x\n"})
	if _, err := os.Stat(filepath.Join(dir, "sub")); err == nil {
		t.Fatal("write escaped the sandbox root")
	}
	if _, err := os.Stat(sandboxFile(dir, "sub/inner.txt")); err != nil {
		t.Fatalf("write did not land in the sandbox: %v", err)
	}
}

func TestContainerWorkspaceSpellingIsMapped(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()

	got := WriteFile(ctx, map[string]interface{}{"path": "/workspace/notes.txt", "content": "hi\n"})
	if !strings.HasPrefix(got, "Wrote") {
		t.Fatalf("container spelling rejected: %q", got)
	}
	if readSandbox(t, dir, "notes.txt") != "hi\n" {
		t.Fatal("container spelling did not map into the sandbox")
	}
}

func TestReadFileRejectsBinary(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(dir, sandboxRootName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sandboxFile(dir, "bin.dat"), []byte{0xff, 0xfe, 0x00}, 0644); err != nil {
		t.Fatal(err)
	}
	got := ReadFile(ctx, map[string]interface{}{"path": "bin.dat"})
	if !strings.Contains(got, "not valid UTF-8") {
		t.Fatalf("expected binary rejection, got %q", got)
	}
}

func TestEditFileUniqueAnchor(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "a.go", "content": "package main\n\nfunc main() {\n\tprintln(1)\n}\n"})

	got := EditFile(ctx, map[string]interface{}{
		"path":       "a.go",
		"old_string": "\tprintln(1)",
		"new_string": "\tprintln(2)",
	})
	if !strings.HasPrefix(got, "Edited a.go") {
		t.Fatalf("edit failed: %q", got)
	}
	if !strings.Contains(got, "- \tprintln(1)") || !strings.Contains(got, "+ \tprintln(2)") {
		t.Fatalf("missing diff: %q", got)
	}
	if !strings.Contains(readSandbox(t, dir, "a.go"), "println(2)") {
		t.Fatal("not written")
	}
}

func TestEditFileRejectsMissingAndAmbiguous(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "a.go", "content": "x := 1\ny := 1\n"})

	got := EditFile(ctx, map[string]interface{}{"path": "a.go", "old_string": "nope", "new_string": "z"})
	if !strings.Contains(got, "not found") {
		t.Fatalf("expected not-found error, got %q", got)
	}

	got = EditFile(ctx, map[string]interface{}{"path": "a.go", "old_string": ":= 1", "new_string": ":= 2"})
	if !strings.Contains(got, "matched 2 times") {
		t.Fatalf("expected ambiguity error, got %q", got)
	}
	if readSandbox(t, dir, "a.go") != "x := 1\ny := 1\n" {
		t.Fatal("ambiguous edit still wrote")
	}

	got = EditFile(ctx, map[string]interface{}{"path": "a.go", "old_string": ":= 1", "new_string": ":= 2", "replace_all": true})
	if !strings.HasPrefix(got, "Edited a.go") {
		t.Fatalf("replace_all failed: %q", got)
	}
	if readSandbox(t, dir, "a.go") != "x := 2\ny := 2\n" {
		t.Fatalf("replace_all wrong: %q", readSandbox(t, dir, "a.go"))
	}
}

func TestEditFileDryRunDoesNotWrite(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "hello world\n"})

	got := EditFile(ctx, map[string]interface{}{
		"path": "a.txt", "old_string": "world", "new_string": "there", "dry_run": true,
	})
	if !strings.HasPrefix(got, "Dry run:") {
		t.Fatalf("expected dry run, got %q", got)
	}
	if readSandbox(t, dir, "a.txt") != "hello world\n" {
		t.Fatal("dry run wrote")
	}
}

func TestEditFilePreservesFileMode(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "s.sh", "content": "#!/bin/sh\necho a\n"})
	if err := os.Chmod(sandboxFile(dir, "s.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	EditFile(ctx, map[string]interface{}{"path": "s.sh", "old_string": "echo a", "new_string": "echo b"})

	info, err := os.Stat(sandboxFile(dir, "s.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode changed to %v", info.Mode().Perm())
	}
}

func TestEditFileCreatesNoTempLeftovers(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "abc\n"})
	EditFile(ctx, map[string]interface{}{"path": "a.txt", "old_string": "abc", "new_string": "xyz"})

	entries, _ := os.ReadDir(filepath.Join(dir, sandboxRootName))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".x01-write-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(sandboxFile(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
}
