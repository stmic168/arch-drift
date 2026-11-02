package codemodel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const fixture = "../../testdata/shop"

func TestScanFixture(t *testing.T) {
	m, err := Scan(fixture, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Module != "example.com/shop" {
		t.Fatalf("module = %q", m.Module)
	}
	wantIDs := []string{"cmd/api", "cmd/worker", "ext:kafka", "ext:postgres", "internal/catalog", "internal/orders", "internal/payments", "internal/store", "pkg/metrics"}
	if got := m.IDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("IDs = %v, want %v", got, wantIDs)
	}

	type e struct{ from, to, ev string }
	want := []e{
		{"cmd/api", "ext:kafka", "cmd/api/main.go:12"},
		{"cmd/api", "internal/catalog", "cmd/api/main.go:8"},
		{"cmd/api", "internal/orders", "cmd/api/main.go:9"},
		{"cmd/api", "internal/store", "cmd/api/main.go:10"},
		{"cmd/api", "pkg/metrics", "cmd/api/main.go:11"},
		{"cmd/worker", "ext:kafka", "cmd/worker/main.go:7"},
		{"cmd/worker", "internal/orders", "cmd/worker/main.go:9"},
		{"cmd/worker", "internal/store", "cmd/worker/main.go:10"},
		{"internal/orders", "ext:postgres", "internal/orders/repo/repo.go:5"},
		{"internal/orders", "internal/payments", "internal/orders/orders.go:6"},
		{"internal/orders", "internal/store", "internal/orders/orders.go:7"},
		{"internal/store", "ext:postgres", "internal/store/store.go:7"},
	}
	var got []e
	for _, ed := range m.Edges {
		if len(ed.Evidence) != 1 {
			t.Errorf("edge %s->%s evidence = %v, want exactly one site", ed.From, ed.To, ed.Evidence)
			continue
		}
		got = append(got, e{ed.From, ed.To, ed.Evidence[0].String()})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges:\n got  %v\n want %v", got, want)
	}
}

func TestScanRollsUpSubpackagesAndSkipsNoise(t *testing.T) {
	m, err := Scan(fixture, Options{})
	if err != nil {
		t.Fatal(err)
	}
	orders, ok := m.Component("internal/orders")
	if !ok {
		t.Fatal("internal/orders missing")
	}
	if want := []string{"internal/orders", "internal/orders/repo"}; !reflect.DeepEqual(orders.Packages, want) {
		t.Errorf("packages = %v, want %v", orders.Packages, want)
	}
	// catalog_test.go and internal/catalog/testdata import store; both must be ignored.
	if _, ok := m.Edge("internal/catalog", "internal/store"); ok {
		t.Error("edge from _test.go or testdata/ leaked into the model")
	}
	// vendor/ and tools/ (outside cmd, internal, pkg) are not components.
	for _, c := range m.Components {
		for _, p := range c.Packages {
			if p == "tools" || len(p) >= 6 && p[:6] == "vendor" {
				t.Errorf("unexpected package %s", p)
			}
		}
	}
	pg, _ := m.Component("ext:postgres")
	if !pg.External || pg.Anchor.String() != "internal/orders/repo/repo.go:5" {
		t.Errorf("ext:postgres = %+v", pg)
	}
}

func TestExternalID(t *testing.T) {
	cases := map[string]string{
		"github.com/jackc/pgx/v5":            "ext:postgres",
		"github.com/jackc/pgx/v5/pgxpool":    "ext:postgres",
		"github.com/lib/pq":                  "ext:postgres",
		"github.com/redis/go-redis/v9":       "ext:redis",
		"github.com/go-redis/redis/v8":       "ext:redis",
		"go.mongodb.org/mongo-driver/mongo":  "ext:mongodb",
		"github.com/segmentio/kafka-go":      "ext:kafka",
		"github.com/IBM/sarama":              "ext:kafka",
		"github.com/Shopify/sarama":          "ext:kafka",
		"google.golang.org/grpc":             "ext:grpc",
		"google.golang.org/grpc/credentials": "ext:grpc",
		"google.golang.org/grpcx":            "",
		"github.com/lib/pqx":                 "",
		"fmt":                                "",
	}
	for in, want := range cases {
		if got := ExternalID(in); got != want {
			t.Errorf("ExternalID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComponentOf(t *testing.T) {
	cases := map[string]string{
		"cmd/api": "cmd/api", "internal/orders/repo": "internal/orders", "pkg/x/y/z": "pkg/x",
		"cmd": "", ".": "", "tools": "", "api/v1": "",
	}
	for in, want := range cases {
		if got := ComponentOf(in); got != want {
			t.Errorf("ComponentOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanWithOptions(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod":                          "module example.com/mono\n",
		"services/billing/main.go":        "package main\n\nimport (\n\t_ \"example.com/mono/services/billing/store\"\n\t_ \"example.com/mono/libs/money\"\n)\n\nfunc main() {}\n",
		"services/billing/store/store.go": "package store\n\nimport _ \"github.com/aws/aws-sdk-go-v2/service/s3\"\n",
		"libs/money/money.go":             "package money\n\nimport _ \"example.com/mono/gen/pb\"\n",
		"gen/pb/pb.go":                    "package pb\n",
		"internal/old/old.go":             "package old\n",
	})
	opts := Options{
		Components: []ComponentRule{{ID: "billing", Paths: []string{"services/billing"}}, {ID: "money", Paths: []string{"libs/money"}}},
		External:   []ExternalRule{{ID: "ext:s3", Prefixes: []string{"github.com/aws/aws-sdk-go-v2/service/s3"}}},
		Exclude:    []string{"gen"},
	}
	m, err := Scan(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.IDs(), []string{"billing", "ext:s3", "internal/old", "money"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs = %v, want %v", got, want)
	}
	if !m.IsDeployable("billing") || m.IsDeployable("money") {
		t.Error("deployable must come from package main")
	}
	for _, e := range [][2]string{{"billing", "ext:s3"}, {"billing", "money"}} {
		if _, ok := m.Edge(e[0], e[1]); !ok {
			t.Errorf("missing edge %v", e)
		}
	}
	if len(m.Edges) != 2 {
		t.Errorf("edges = %+v (gen/ is excluded, so money -> gen must not appear)", m.Edges)
	}

	opts.NoDefaultLayout = true
	m, _ = Scan(repo, opts)
	if _, ok := m.Component("internal/old"); ok {
		t.Error("NoDefaultLayout must drop internal/old")
	}
}
