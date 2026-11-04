# How it works

[README](../../README.md) · [Usage](usage.md) · [Configuration](configuration.md) · [CI and pull requests](ci.md) · [How it works](how-it-works.md) · [Development](development.md)

## What is sent to the API

The system prompt, the doc text, and the list of component IDs (e.g. `cmd/api`, `internal/orders`, `ext:postgres`). No source code, file paths or import sites leave the machine.

## Pipeline

1. **Code model** (`internal/codemodel`). Parses every `.go` file with `go/parser` (imports only), skipping `_test.go`, `vendor/`, `testdata/`, hidden dirs and configured `exclude` dirs below the root. Components are configured `components`, then `cmd/x`, `internal/x`, `pkg/x`; subpackages roll up (`internal/orders/repo` belongs to `internal/orders`). Edges are imports of the module's own packages, with every `file:line`. External drivers map to catalog IDs:

   | Import prefix | ID |
   |---|---|
   | `github.com/jackc/pgx`, `github.com/lib/pq` | `ext:postgres` |
   | `github.com/redis/go-redis`, `github.com/go-redis/redis` | `ext:redis` |
   | `go.mongodb.org/mongo-driver` | `ext:mongodb` |
   | `github.com/segmentio/kafka-go`, `github.com/IBM/sarama`, `github.com/Shopify/sarama` | `ext:kafka` |
   | `google.golang.org/grpc` | `ext:grpc` |
   | `github.com/gocql/gocql`, `github.com/apache/cassandra-gocql-driver` | `ext:cassandra` |
   | `github.com/go-sql-driver/mysql` | `ext:mysql` |
   | `modernc.org/sqlite`, `github.com/mattn/go-sqlite3` | `ext:sqlite` |
   | `github.com/elastic/go-elasticsearch`, `github.com/olivere/elastic` | `ext:elasticsearch` |
   | `github.com/thanos-io/objstore` | `ext:objstore` |
   | `github.com/aws/aws-sdk-go-v2/service/s3`, `github.com/aws/aws-sdk-go/service/s3`, `github.com/minio/minio-go` | `ext:s3` |
   | `cloud.google.com/go/storage` | `ext:gcs` |
   | `github.com/Azure/azure-sdk-for-go/sdk/storage/azblob` | `ext:azure-blob` |
   | `github.com/nats-io/nats.go` | `ext:nats` |
   | `github.com/rabbitmq/amqp091-go`, `github.com/streadway/amqp` | `ext:rabbitmq` |
   | `go.etcd.io/etcd/client` | `ext:etcd` |
   | `github.com/hashicorp/consul/api` | `ext:consul` |
   | `github.com/bradfitz/gomemcache` | `ext:memcached` |

   Generated protobuf packages and configured `shared` directories create no edges. An import of another component's shared package is kept as a *protocol edge*: it proves a required relation (a querier importing the frontend's gRPC client depends on the frontend) but is never an undocumented dependency. The code model also records coverage: how many Go packages belong to a component. `check` adds a note when it is under 50%, naming the largest unmodeled directories.

2. **Doc model** (`internal/docmodel`). One Messages API call with structured output (`output_config.format`, JSON schema). Input: the doc (wrapped in `<design_doc>` tags and treated as untrusted data by the system prompt) plus the ID list. Output: `components[]` (`name`, `code_ids[]`, `quote`) and `relations[]` (`from`, `to`, `kind` = `required|forbidden`, `protocol`, `quote`). The LLM sits behind the `Extractor` interface; implementations are the live `AnthropicExtractor`, `FileExtractor` (`--doc-model`) and `CachedExtractor`.
   - Cache: `<out>/cache/<sha256(model, IDs, doc)>.json`. A hit costs nothing and is reported as source `cache`.
   - `stop_reason: refusal` and `max_tokens` are errors (exit 2) with the refusal category in the message.
   - Fallbacks: the Go SDK (`anthropic-sdk-go/v2` v2.4.0) has `BetaFallbacksParamUnion.OfDefault`, so requests send `fallbacks: "default"` with beta header `server-side-fallback-2026-07-01`. A refused request is re-served server-side by the model Anthropic recommends for the refusal category; the decoder reads the text after the last `fallback` block. This works on the first-party Claude API only (not Bedrock, Vertex or Foundry).
3. **Verifier**. A claim's quote must appear in the doc after whitespace normalization, or the claim is dropped. Unknown `code_ids` are dropped. Relations pointing at a dropped component, with an invalid kind, or with a fake quote are dropped. Every dropped claim is listed in the report.
4. **Diff** (`internal/drift`):

   | Type | Severity | Fails | Rule |
   |---|---|---|---|
   | `FORBIDDEN_EDGE` | high | yes | doc says A must not depend on B, and code has a direct import A to B |
   | `MISSING_COMPONENT` | medium | yes | doc component maps to no code ID |
   | `MISSING_EDGE` | medium | yes | doc requires A to depend on B, the relation can be checked by static import, and there is no direct import |
   | `UNDOCUMENTED_EDGE` | low | yes | direct import between two documented components (in different doc components) that no doc relation covers |
   | `UNDOCUMENTED_COMPONENT` | info (low when configured to fail) | no by default | code component that no doc component maps to, ranked by how many components import it |
   | `UNVERIFIABLE` | info | no | relation that imports cannot prove or disprove: the target is a deployable (a main package cannot be imported); the protocol is a runtime transport (HTTP, gRPC, Kafka, queue, ...) and there is no import; or one endpoint has no code (missing, or external by design) |

   Doc components the model marks `external` (users, SDK workers, dashboards, managed services) are not reported as `MISSING_COMPONENT` when they have no code ID. Relations to `ext:*` are always checkable: the driver import is the evidence, directly or through a client wrapper (`store -> cacheutil -> memcached` satisfies "Store uses Memcached"). A required relation is also satisfied by a protocol edge. The protocol `network` (separately running nodes that talk without a named protocol) counts as a runtime transport. By default only direct imports count; `transitive` in the config extends that to import paths. Edges touching an undocumented component are not reported again as `UNDOCUMENTED_EDGE`.
5. **Report** (`internal/report`): `report.md` (findings with evidence, suggested doc updates, an as-built diagram, a coloured drift diagram, dropped claims, token usage and estimated cost), `report.json` and `report.sarif`.

   **Suggested doc updates** turn each finding into a concrete edit, generated from the evidence without an LLM: which import to remove for a forbidden dependency, which doc sentence to update for a missing one, what to describe for an undocumented dependency, and the top application-like undocumented components (those that import more components than import them). The **as-built diagram** is a Mermaid flowchart of the documented components with the dependencies the code has between them today, under the doc's names (gRPC-client-only links dashed), ready to paste into the doc. Output has no timestamps and is byte-identical across runs on the same inputs.

## Limitations

- The doc model comes from an LLM and can differ between uncached runs; the cache makes repeated runs on the same doc stable.
- Cost is an estimate from list prices (cache writes 1.25x input, cache reads 0.1x input). With a server-side fallback, it is priced at the serving model's rate.
- Without a config, packages outside `cmd/`, `internal/`, `pkg/` are ignored.
