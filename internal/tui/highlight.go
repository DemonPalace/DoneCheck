package tui

import (
	"bytes"
	"os"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlight returns exactly n ANSI lines, each reset-terminated so colour cannot leak into the border; nil means render plain.
func highlight(filename, text string, n int) []string {
	if os.Getenv("NO_COLOR") != "" {
		return nil
	}
	lexer := lexers.Match(filename)
	if lexer == nil {
		return nil
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return nil
	}
	formatter, style := formatters.Get("terminal16"), styles.Get("monokai")
	lines := chroma.SplitTokensIntoLines(it.Tokens())
	if len(lines) < n {
		return nil
	}
	out := make([]string, 0, n)
	for _, tokens := range lines[:n] {
		var buf bytes.Buffer
		if err := formatter.Format(&buf, style, chroma.Literator(tokens...)); err != nil {
			return nil
		}
		out = append(out, string(bytes.TrimRight(buf.Bytes(), "\n"))+"\x1b[0m")
	}
	return out
}
