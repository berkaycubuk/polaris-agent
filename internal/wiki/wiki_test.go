package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"Hello World", []string{"hello", "world"}},
		{"the quick brown fox", []string{"quick", "brown", "fox"}},
		{"A", []string{}}, // too short
		{"how to deploy", []string{"deploy"}},
		{"UPPER CASE", []string{"upper", "case"}},
		{"foo-bar baz", []string{"foo", "bar", "baz"}},
		{"123 numbers 456", []string{"123", "numbers", "456"}},
	}
	for _, tc := range tests {
		got := tokenize(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("tokenize(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestChunkText(t *testing.T) {
	// Single short paragraph
	chunks := chunkText("short text")
	if len(chunks) != 1 || chunks[0] != "short text" {
		t.Fatalf("got %v", chunks)
	}

	// Multiple paragraphs that fit in one chunk
	text := "para one\n\npara two\n\npara three"
	chunks = chunkText(text)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// Many paragraphs to force splitting
	var paragraphs []string
	for i := 0; i < 50; i++ {
		paragraphs = append(paragraphs, strings.Repeat("word ", 50))
	}
	longText := strings.Join(paragraphs, "\n\n")
	chunks = chunkText(longText)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long text, got %d", len(chunks))
	}

	// Empty paragraphs skipped
	chunks = chunkText("hello\n\n\n\nworld")
	if len(chunks) != 1 {
		t.Fatalf("empty paras should be skipped, got %d chunks", len(chunks))
	}
}

func TestLoadChunks_NonexistentDir(t *testing.T) {
	chunks, err := loadChunks("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestLoadChunks_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	chunks, err := loadChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestLoadChunks_SkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not markdown"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0o644)
	chunks, err := loadChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for non-md files, got %d", len(chunks))
	}
}

func TestLoadChunks_ReadsMarkdown(t *testing.T) {
	dir := t.TempDir()
	content := "# Hello\n\nThis is a test page about deployment.\n\n## Section\n\nMore content here."
	_ = os.WriteFile(filepath.Join(dir, "test.md"), []byte(content), 0o644)
	chunks, err := loadChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if chunks[0].File != "test.md" {
		t.Fatalf("expected file 'test.md', got %q", chunks[0].File)
	}
	if chunks[0].Index != 0 {
		t.Fatalf("expected index 0, got %d", chunks[0].Index)
	}
}

func TestSearch_NoResults(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "doc.md"), []byte("unrelated content"), 0o644)
	results, err := Search(dir, "deployment", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_FindsRelevant(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "deploy.md"), []byte("# Deployment\n\nHow to deploy the application to production servers."), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "cooking.md"), []byte("# Cooking\n\nHow to bake a chocolate cake with frosting."), 0o644)

	results, err := Search(dir, "deploy production", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if !strings.Contains(results[0].Chunk.File, "deploy") {
		t.Fatalf("expected deploy.md first, got %s", results[0].Chunk.File)
	}
	if results[0].Score <= 0 {
		t.Fatalf("expected positive score, got %f", results[0].Score)
	}
}

func TestSearch_TopK(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		content := strings.Repeat("deployment deployment deployment ", 20)
		_ = os.WriteFile(filepath.Join(dir, string(rune('a'+i))+".md"), []byte(content), 0o644)
	}
	results, err := Search(dir, "deployment", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 2 {
		t.Fatalf("expected at most 2 results, got %d", len(results))
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "doc.md"), []byte("content"), 0o644)
	results, err := Search(dir, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestSearch_StopwordsOnly(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "doc.md"), []byte("the a an is are"), 0o644)
	results, err := Search(dir, "the a an", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for stopword-only query, got %d", len(results))
	}
}

func TestFormatResults_Empty(t *testing.T) {
	got := FormatResults(nil)
	if got != "No matching wiki entries found." {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestFormatResults_WithResults(t *testing.T) {
	results := []ScoredChunk{
		{Chunk: Chunk{File: "test.md", Index: 0, Content: "hello world"}, Score: 1.5},
	}
	got := FormatResults(results)
	if !strings.Contains(got, "Result 1") {
		t.Fatalf("missing result header: %s", got)
	}
	if !strings.Contains(got, "test.md") {
		t.Fatalf("missing file name: %s", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Fatalf("missing content: %s", got)
	}
	if !strings.Contains(got, "1.50") {
		t.Fatalf("missing score: %s", got)
	}
}
