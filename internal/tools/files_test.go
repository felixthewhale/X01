package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestWriteFileCreatesAndRefusesOverwrite(t *testing.T) {
	chdirTemp(t)
	ctx := context.Background()

	got := WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "hello\n"})
	if !strings.HasPrefix(got, "Wrote a.txt") {
		t.Fatalf("unexpected write result: %q", got)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "hello\n" {
		t.Fatalf("content = %q", string(b))
	}

	got = WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "clobber\n"})
	if !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "already exists") {
		t.Fatalf("expected refusal, got %q", got)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "hello\n" {
		t.Fatalf("file was clobbered: %q", string(b))
	}

	got = WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "clobber\n", "overwrite": true})
	if !strings.HasPrefix(got, "Wrote a.txt") {
		t.Fatalf("overwrite failed: %q", got)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "clobber\n" {
		t.Fatalf("content = %q", string(b))
	}
}

func TestReadFileWindowAndNumbering(t *testing.T) {
	chdirTemp(t)
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
}

func TestReadFileRejectsEscapeAndBinary(t *testing.T) {
	chdirTemp(t)
	ctx := context.Background()

	got := ReadFile(ctx, map[string]interface{}{"path": "../../etc/passwd"})
	if !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected escape rejection, got %q", got)
	}
	got = ReadFile(ctx, map[string]interface{}{"path": "/etc/passwd"})
	if !strings.HasPrefix(got, "Error:") {
		t.Fatalf("expected absolute path rejection, got %q", got)
	}

	os.WriteFile("bin.dat", []byte{0xff, 0xfe, 0x00}, 0644)
	got = ReadFile(ctx, map[string]interface{}{"path": "bin.dat"})
	if !strings.Contains(got, "not valid UTF-8") {
		t.Fatalf("expected binary rejection, got %q", got)
	}
}

func TestEditFileUniqueAnchor(t *testing.T) {
	chdirTemp(t)
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
	if b, _ := os.ReadFile("a.go"); !strings.Contains(string(b), "println(2)") {
		t.Fatalf("not written: %q", string(b))
	}
}

func TestEditFileRejectsMissingAndAmbiguous(t *testing.T) {
	chdirTemp(t)
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
	if b, _ := os.ReadFile("a.go"); string(b) != "x := 1\ny := 1\n" {
		t.Fatalf("ambiguous edit still wrote: %q", string(b))
	}

	got = EditFile(ctx, map[string]interface{}{"path": "a.go", "old_string": ":= 1", "new_string": ":= 2", "replace_all": true})
	if !strings.HasPrefix(got, "Edited a.go") {
		t.Fatalf("replace_all failed: %q", got)
	}
	if b, _ := os.ReadFile("a.go"); string(b) != "x := 2\ny := 2\n" {
		t.Fatalf("replace_all wrong: %q", string(b))
	}
}

func TestEditFileDryRunDoesNotWrite(t *testing.T) {
	chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "a.txt", "content": "hello world\n"})

	got := EditFile(ctx, map[string]interface{}{
		"path": "a.txt", "old_string": "world", "new_string": "there", "dry_run": true,
	})
	if !strings.HasPrefix(got, "Dry run:") {
		t.Fatalf("expected dry run, got %q", got)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "hello world\n" {
		t.Fatalf("dry run wrote: %q", string(b))
	}
}

func TestEditFilePreservesFileMode(t *testing.T) {
	chdirTemp(t)
	ctx := context.Background()
	WriteFile(ctx, map[string]interface{}{"path": "s.sh", "content": "#!/bin/sh\necho a\n"})
	if err := os.Chmod("s.sh", 0755); err != nil {
		t.Fatal(err)
	}
	EditFile(ctx, map[string]interface{}{"path": "s.sh", "old_string": "echo a", "new_string": "echo b"})

	info, err := os.Stat("s.sh")
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

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".x01-write-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
}
