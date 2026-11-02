package catalog

// Test files are ignored by the scanner: this import must not create an edge.
import (
	"testing"

	"example.com/shop/internal/store"
)

func TestList(t *testing.T) { _ = store.Open("") }
