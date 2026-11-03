# ADR-001: Cortex component boundaries

Status: pilot test document for archdrift (not part of the Cortex project).

This record lists the dependency rules between Cortex components that the
architecture group expects the code to follow.

## Components

- **Distributor** receives samples on the write path and forwards them.
- **Ingester** keeps recent samples in memory and writes blocks.
- **Querier** evaluates PromQL queries on the read path.
- **Store gateway** serves blocks from long-term storage to queriers.
- **Compactor** merges and deduplicates blocks.
- **Ruler** evaluates recording and alerting rules.
- **Hash ring** is the foundational library that shards work between replicas.
- **Storage layer** wraps the block storage backends.
- **Query frontend** splits and caches queries.
- **Query scheduler** queues queries between the query frontend and the queriers.
- **HA tracker** deduplicates samples from replicated Prometheus pairs.
- **Purger** deletes the data of removed tenants.
- **Overrides** exposes per-tenant limits.
- **Configs API** stores per-tenant rule and alertmanager configuration.

## Rules that must hold

1. The Distributor sends samples to the Ingester.
2. The Querier fetches blocks through the Store gateway.
3. The Ruler evaluates rules through the Querier.
4. The Compactor reads and writes blocks in the Storage layer.
5. The Query frontend forwards queries to the Query scheduler.
6. The Purger must coordinate with the Compactor before deleting tenant blocks.
7. The Overrides component reads per-tenant limits from the Configs API.

## Prohibited dependencies

8. The Hash ring is a foundational library and must not depend on the Querier.
9. The Storage layer must not depend on the Ingester.
10. The Distributor must not depend on the Querier: the write path stays independent of the read path.
11. The Distributor must never talk to the Store gateway.
12. The Compactor must not depend on the Querier.
13. The Ingester must not depend on the Store gateway.
