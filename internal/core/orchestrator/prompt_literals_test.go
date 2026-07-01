package orchestrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenPromptLiterals are prose fragments that must live in internal/prompts/internal,
// not as raw string literals in orchestrator production code.
var forbiddenPromptLiterals = []string{
	"You are the orchestrator mediating between the user",
	"A delegated task is awaiting clarification from you",
	"Background task(s) are running and you cannot advance them",
	"Call `sapaloq_stop` tool to stop continous turn loop",
	"You are a background SapaLOQ task agent",
	"Approved plan to execute (read it as authoritative",
	"Avoid repeating these mistakes the user flagged",
	"Prefetched context (from memory index",
	"Your previous tool call(s) could not be parsed",
	"Resume this task from the conversation below",
	"Summarize the following conversation transcript",
}

func TestOrchestratorNoRawPromptLiterals(t *testing.T) {
	root := "."
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		for _, lit := range collectStringLiterals(f) {
			for _, forbidden := range forbiddenPromptLiterals {
				if strings.Contains(lit, forbidden) {
					t.Errorf("%s contains forbidden prompt literal %q (use internal/prompts instead)", path, forbidden)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func collectStringLiterals(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconvUnquote(lit.Value); err == nil && len(v) > 40 {
			out = append(out, v)
		}
		return true
	})
	return out
}

func strconvUnquote(s string) (string, error) {
	if len(s) < 2 {
		return "", os.ErrInvalid
	}
	if s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `\"`, `"`), nil
	}
	if s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1], nil
	}
	return "", os.ErrInvalid
}
