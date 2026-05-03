package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// approx tokens per chunk (~500 tokens ≈ ~2000 chars in English)
const chunkCharTarget = 2000

type Chunk struct {
	File    string
	Index   int
	Content string
}

type ScoredChunk struct {
	Chunk Chunk
	Score float64
}

func Search(wikiDir, query string, topK int) ([]ScoredChunk, error) {
	chunks, err := loadChunks(wikiDir)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	// document frequency for IDF
	df := map[string]int{}
	docTokens := make([][]string, len(chunks))
	for i, ch := range chunks {
		toks := tokenize(ch.Content)
		docTokens[i] = toks
		seen := map[string]bool{}
		for _, t := range toks {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
	N := float64(len(chunks))

	scored := make([]ScoredChunk, 0, len(chunks))
	for i, ch := range chunks {
		tf := map[string]int{}
		for _, t := range docTokens[i] {
			tf[t]++
		}
		var score float64
		for _, q := range queryTerms {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			idf := 0.0
			if df[q] > 0 {
				idf = 1.0 + (N / float64(df[q]))
			}
			score += f * idf
		}
		if score > 0 {
			scored = append(scored, ScoredChunk{Chunk: ch, Score: score})
		}
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored, nil
}

func loadChunks(wikiDir string) ([]Chunk, error) {
	if _, err := os.Stat(wikiDir); os.IsNotExist(err) {
		return nil, nil
	}
	var chunks []Chunk
	err := filepath.WalkDir(wikiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(wikiDir, path)
		for i, c := range chunkText(string(data)) {
			chunks = append(chunks, Chunk{File: rel, Index: i, Content: c})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

// chunkText splits text into ~chunkCharTarget chunks at paragraph boundaries.
func chunkText(text string) []string {
	paras := strings.Split(text, "\n\n")
	var chunks []string
	var cur strings.Builder
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if cur.Len()+len(p)+2 > chunkCharTarget && cur.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(cur.String()))
	}
	return chunks
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"to": true, "of": true, "in": true, "on": true, "at": true, "for": true,
	"with": true, "by": true, "from": true, "as": true, "it": true, "this": true,
	"that": true, "these": true, "those": true, "i": true, "you": true, "he": true,
	"she": true, "we": true, "they": true, "what": true, "which": true, "who": true,
	"when": true, "where": true, "how": true, "do": true, "does": true, "did": true,
	"have": true, "has": true, "had": true, "will": true, "would": true, "can": true,
	"could": true, "should": true, "if": true, "not": true, "no": true,
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if stopwords[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

func FormatResults(results []ScoredChunk) string {
	if len(results) == 0 {
		return "No matching wiki entries found."
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "--- Result %d (file: %s, chunk: %d, score: %.2f) ---\n%s\n\n",
			i+1, r.Chunk.File, r.Chunk.Index, r.Score, r.Chunk.Content)
	}
	return strings.TrimSpace(b.String())
}
