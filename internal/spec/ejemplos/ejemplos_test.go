package ejemplos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rendimiento-ai/rendimiento/internal/spec"
)

// Every example, and the complete example in the book, is valid.
func TestValidos(t *testing.T) {
	todos, err := Todos()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range todos {
		if err := e.Spec.Validate(); err != nil {
			t.Errorf("%s: %v", e.Nombre, err)
		}
	}
	book, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "content", "guide", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(book), "```yaml\n")
	block, _, ok2 := strings.Cut(rest, "```")
	if !ok || !ok2 {
		t.Fatal("no yaml example in the book's spec page")
	}
	s, err := spec.Parse([]byte(block))
	if err != nil {
		t.Fatalf("the book's complete example: %v", err)
	}
	s.Default()
	if err := s.Validate(); err != nil {
		t.Fatalf("the book's complete example: %v", err)
	}
}
