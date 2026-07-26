# handler

HTTP API handlers that translate [Loki API requests](https://grafana.com/docs/loki/latest/reference/loki-http-api/) into journald queries.

## Endpoint coverage

### Implemented

| Endpoint | Method | Description |
|---|---|---|
| `/loki/api/v1/query_range` | GET | Range queries over log streams |
| `/loki/api/v1/query` | GET | Instant metric queries (vector) |
| `/loki/api/v1/labels` | GET | List known labels |
| `/loki/api/v1/label/<name>/values` | GET | List values for a label |
| `/loki/api/v1/series` | GET | List matching label sets |
| `/loki/api/v1/index/stats` | GET | Approximate stream/chunk/entry/byte counts |
| `/ready` | GET | Readiness probe (always 200) |

### Not implemented

These endpoints are out of scope for a query-only journald bridge:

| Endpoint | Reason |
|---|---|
| `POST /loki/api/v1/push` | Query-only; no storage or ingestion |
| `POST /otlp/v1/logs` | Query-only; no storage or ingestion |
| `/loki/api/v1/tail` | WebSocket streaming; may be added later |
| `/loki/api/v1/patterns` | Requires pattern ingester; not applicable |
| `/loki/api/v1/index/volume` | Requires index volume tracking |
| `/loki/api/v1/index/volume_range` | Requires index volume tracking |
| `/loki/api/v1/detected_fields` | Requires field detection across streams |
| `/loki/api/v1/delete` | No storage to delete from |
| `/loki/api/v1/rules` | Ruler component; not applicable |
| `/loki/api/v1/format_query` | LogQL parser; out of scope |
| Ring, flush, shutdown endpoints | Microservices internals; not applicable |

## LogQL support

Journald provides structured key-value fields natively, so full LogQL pipeline support isn't needed for most queries. The supported subset:

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
```

### Label filters (partial support)

Can filter on labels that exist as journald fields without requiring a parser stage:

```
{job="sshd"} | PRIORITY="4"
```

Pipeline parsers (`| json`, `| logfmt`, `| pattern`) are not supported. Journald already provides structured fields, so most use cases are covered by direct label filtering.

### Metric queries (partial support)

Basic aggregation functions that can be answered by counting entries in time windows:

- `count_over_time({job="sshd"}[5m])`
- `rate({job="sshd"}[5m])`
- `sum`, `avg`, `min`, `max` over the above

Complex metric queries involving multiple label combinations or subqueries are not supported.

## Query translation

Each incoming Loki query is translated to journald operations as follows:

1. **Stream selector** -> `journal.OpenJournal` + entry iteration with field matching
2. **Time range** -> `File.SeekRealtime(start)` to position at the start time, then iterate until `end`
3. **Line filters** -> In-memory string matching on `Entry.Message()`
4. **Limit** -> Stop iteration after N matching entries
5. **Direction** -> Forward iteration (oldest first) or reverse (newest first via `SeekTail` + reverse scan)

### Label queries

- `GET /loki/api/v1/labels` returns all distinct field names across the available journal files
- `GET /loki/api/v1/label/<name>/values` returns all distinct values for the named field

**Note:** The `start` and `end` time range parameters on the labels and label values endpoints are accepted for API compatibility but ignored. Results reflect all labels and values present in the available journal files, not just those within the requested time range.

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
handler/
├── handler.go      # HTTP handler registration and middleware
├── query.go        # /loki/api/v1/query and /loki/api/v1/query_range
├── labels.go       # /loki/api/v1/labels and /loki/api/v1/label/<name>/values
├── series.go       # /loki/api/v1/series
├── stats.go        # /loki/api/v1/index/stats
├── ready.go        # /ready
└── model.go        # Loki API response types
```

The handler package exposes an `http.Handler` (or `http.HandlerFunc`) that can be mounted on any `http.ServeMux` or embedded in other applications. It depends only on the `journal` package and the Go standard library.
