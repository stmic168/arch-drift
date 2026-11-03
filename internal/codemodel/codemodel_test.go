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
		"libs/money/tool/main.go":         "package main\n\nfunc main() {}\n",
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
		t.Error("deployable must come from a main package at the component root, not a nested tool")
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

func TestSharedPackagesAndCoverage(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod":                         "module example.com/x\n",
		"cmd/api/main.go":                "package main\n\nimport (\n\t_ \"example.com/x/pkg/store/storepb\"\n\t_ \"example.com/x/pkg/query/stats\"\n\t_ \"example.com/x/pkg/query\"\n)\n\nfunc main() {}\n",
		"pkg/store/store.go":             "package store\n",
		"pkg/store/storepb/types.pb.go":  "package storepb\n",
		"pkg/query/query.go":             "package query\n",
		"pkg/query/stats/stats.go":       "package stats\n",
		"service/history/history.go":     "package history\n",
		"service/history/shard/shard.go": "package shard\n",
		"service/matching/matching.go":   "package matching\n",
	})
	m, err := Scan(repo, Options{Shared: []string{"pkg/query/stats"}})
	if err != nil {
		t.Fatal(err)
	}
	// storepb (protocol rule) and query/stats (configured) create no edges.
	if len(m.Edges) != 1 || m.Edges[0].To != "pkg/query" {
		t.Fatalf("edges = %+v, want only cmd/api -> pkg/query", m.Edges)
	}
	want := Coverage{Packages: 8, Modeled: 3, Shared: []string{"pkg/query/stats", "pkg/store/storepb"},
		Unmodeled: []DirCount{{Dir: "service", Packages: 3}}}
	if !reflect.DeepEqual(m.Coverage, want) || m.Coverage.Percent() != 37 {
		t.Fatalf("coverage = %+v (%d%%)", m.Coverage, m.Coverage.Percent())
	}
	// The protocol rule can be turned off.
	m, _ = Scan(repo, Options{NoSharedProtocol: true})
	if _, ok := m.Edge("cmd/api", "pkg/store"); !ok {
		t.Error("with NoSharedProtocol, storepb rolls up into pkg/store again")
	}
}

func TestIsShared(t *testing.T) {
	o := Options{}
	for dir, want := range map[string]bool{"pkg/store/storepb": true, "pkg/cortexpb": true, "api/v1pb": true, "pkg/pb": false, "pkg/grpc": false, "pkg/Storepb": false, "pkg/store": false} {
		if got := o.IsShared(dir); got != want {
			t.Errorf("IsShared(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestScanFollowsSymlinkedRoot(t *testing.T) {
	link := filepath.Join(t.TempDir(), "target")
	abs, _ := filepath.Abs(fixture)
	if err := os.Symlink(abs, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	m, err := Scan(link, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Components) != 9 {
		t.Fatalf("components via symlink = %d, want 9", len(m.Components))
	}
}

func TestProtocolPackages(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod":                               "module example.com/x\n",
		"service/history/history.go":           "package history\n\nimport _ \"example.com/x/api/matchingservice/v1\"\n",
		"service/matching/matching.go":         "package matching\n",
		"api/matchingservice/v1/service.pb.go": "package matchingservice\n",
		"api/matchingservice/v1/helpers.go":    "package matchingservice\n",
		"service/billing/billing.pb.go":        "package billing\n",
		"service/history/client/client.go":     "package client\n\nimport _ \"example.com/x/service/billing\"\n",
	})
	opts := Options{Components: []ComponentRule{
		{ID: "history", Paths: []string{"service/history"}},
		{ID: "matching", Paths: []string{"service/matching", "api/matchingservice"}},
		{ID: "billing", Paths: []string{"service/billing"}},
	}}
	m, err := Scan(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	// api/matchingservice/v1 holds *.pb.go below matching's root: shared, so no
	// edge, but a protocol edge history -> matching.
	if _, ok := m.Edge("history", "matching"); ok {
		t.Error("a protocol package import must not create a dependency edge")
	}
	if e, ok := m.ProtocolEdge("history", "matching"); !ok || e.Evidence[0].String() != "service/history/history.go:3" {
		t.Errorf("protocol edge = %+v %v", e, ok)
	}
	// Generated code at a component root is the component itself, not shared.
	if _, ok := m.Edge("history", "billing"); !ok {
		t.Error("service/billing has *.pb.go at its root and must stay a normal component")
	}
	if !reflect.DeepEqual(m.Coverage.Shared, []string{"api/matchingservice/v1"}) {
		t.Errorf("shared = %v", m.Coverage.Shared)
	}
}
