// Package catalog serves the product catalogue.
// planted MISSING_EDGE: the doc says Catalog reads products from Store, but it does not import it.
package catalog

type Catalog struct{ items []string }

func New() *Catalog { return &Catalog{items: []string{"book", "pen"}} }

func (c *Catalog) List() []string { return c.items }
