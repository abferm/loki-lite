# loki-lite

Query-only Loki-compatible API backed by journald. Translates [Loki API requests](https://grafana.com/docs/loki/latest/reference/loki-http-api/) into journald journal operations.

## Endpoint coverage

### Implemented

| Endpoint | Method | Description |
|---|---|---|
| `/loki/api/v1/query_range` | GET | Range queries over log streams |
| `/loki/api/v1/query` | GET | Instant queries (vector for metrics, streams for logs) |
| `/loki/api/v1/labels` | GET | List known labels |
| `/loki/api/v1/label/<name>/values` | GET | List values for a label |
| `/loki/api/v1/series` | GET | List matching label sets |
| `/loki/api/v1/index/stats` | GET | Approximate stream/chunk/entry/byte counts |
| `/loki/api/v1/format_query` | GET | Format and validate a LogQL query |
| `/loki/api/v1/tail` | GET | WebSocket streaming of log entries (with catch-up from start) |
| `/ready` | GET | Readiness probe (always 200) |

### Not implemented

These endpoints are out of scope for a query-only journald bridge:

| Endpoint | Reason |
|---|---|
| `POST /loki/api/v1/push` | Query-only; no storage or ingestion |
| `POST /otlp/v1/logs` | Query-only; no storage or ingestion |
| `/loki/api/v1/patterns` | Requires pattern ingester; not applicable |
| `/loki/api/v1/index/volume` | Requires index volume tracking |
| `/loki/api/v1/index/volume_range` | Requires index volume tracking |
| `/loki/api/v1/detected_fields` | Requires field detection across streams |
| `/loki/api/v1/delete` | No storage to delete from |
| `/loki/api/v1/rules` | Ruler component; not applicable |
| Ring, flush, shutdown endpoints | Microservices internals; not applicable |

## LogQL support

Journald provides structured key-value fields natively, so full LogQL pipeline support isn't needed for most queries. However, the imported Loki `logql` library provides full parsing and pipeline execution, so nearly all LogQL syntax is supported.

### Stream selectors (full support)

Journald fields map directly to Loki labels. Every field returned by `journal.Entry.Fields` is available as a label.

```
{job="sshd"}
{_SYSTEMD_UNIT="sshd.service", PRIORITY="4"}
{HOSTNAME=~"web-.*"}
```

Supported matchers: `=`, `!=`, `=~`, `!~`

### Line filters (full support)

Post-read string matching on the log line (`MESSAGE` field):

```
{job="sshd"} |= "Accepted"
{job="sshd"} !~ "password"
{job="sshd"} |~ "status=(200|301)"
```

### Label filters (full support)

Filter on labels that exist as journald fields:

```
{job="sshd"} | PRIORITY="4"
{job="sshd"} | json | level="error"
```

### Pipeline parsers (full support)

Full LogQL pipeline parsing via the Loki `logql` library. Since journald already provides structured fields, parsers are most useful for reinterpreting the MESSAGE field or composing with label filters:

```
{job="sshd"} | json
{job="sshd"} | logfmt
{job="sshd"} | pattern "<_> status=<status>"
{job="sshd"} | regexp "status=(?P<status>\d+)"
```

### Line and label formatting (full support)

```
{job="sshd"} | json | line_format "{{.status}}"
{job="sshd"} | json | label_format status="ok"
```

### Metric queries (full support)

Any LogQL metric expression is supported, including `unwrap` and nested aggregations:

- `count_over_time({job="sshd"}[5m])`
- `rate({job="sshd"}[5m])`
- `rate({job="sshd"} | unwrap duration [5m])`
- `sum(count_over_time({job="sshd"}[5m]))`
- `topk(5, count_over_time({job="sshd"}[5m]))`
- `avg(rate({job="sshd"} | unwrap duration [5m]))`

### Format query

`/loki/api/v1/format_query` parses and pretty-prints any LogQL query using the Loki `logql/syntax` parser. Invalid queries return a parse error.

## Query translation

Each incoming Loki query is translated to journald operations as follows:

1. **Stream selector** -> `journal.OpenJournal` + entry iteration with field matching
2. **Time range** -> `File.SeekRealtime(start)` to position at the start time, then iterate until `end`
3. **Pipeline stages** -> Loki `log.Pipeline` processes each entry (line filters, parsers, label filters, formatting)
4. **Metric extractors** -> Loki `log.SampleExtractor` extracts numeric values from matching entries
5. **Limit** -> Stop iteration after N matching entries
6. **Direction** -> Forward iteration (oldest first) or reverse (newest first via `SeekTail` + reverse scan)

### Label queries

- `GET /loki/api/v1/labels` returns all label names present in the journal files, excluding those in the schema's exclude list
- `GET /loki/api/v1/label/<name>/values` returns all distinct values for the named label (capped at 10000 values)

**Note:** The `start` and `end` time range parameters on the labels and label values endpoints are accepted for API compatibility but ignored. Results reflect all labels and values present in the available journal files, not just those within the requested time range.

**Note:** The `match` parameter on `/loki/api/v1/index/stats` is accepted but currently ignored — counts cover all streams in the time range. Per-stream filtering is planned.

### Response format

All responses use the standard Loki JSON envelope:

```json
{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {
        "stream": { "job": "sshd" },
        "values": [
          ["1569266497240578000", "Accepted publickey for root"]
        ]
      }
    ]
  }
}
```

## Architecture

```
model/
├── model.go           # Schema, Entry types, journal-to-Loki conversion

query/
├── log_pipeline.go    # LogQL log selector parsing and pipeline execution
├── metric_pipeline.go # LogQL metric query parsing and sample extraction

engine/
├── engine.go          # Query orchestration, label queries, series, stats

journal/
├── journal.go         # Journald file reading, field hash table, entry iteration
├── file.go            # Low-level journal file format parsing
```

The `query` package wraps the Loki `logql` library to parse and execute LogQL queries against `model.Entry` values. The `engine` package orchestrates journal iteration and delegates filtering/extraction to the query pipeline.
