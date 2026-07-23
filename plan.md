## Purpose of Loki Lite

Loki Lite's purpose is to provide a Loki compatible query interface for journald logs

## Requirements
* query only: Loki Lite will have no storage of it's own and exists only as a query engine for journald logs
* pure go: any main packages within loki-lite should be installable by anyone via `go install ...`
* 1-to-1 compatibility with loki's query interface: I should be able to run queries with grafana's loki source, logcli, and other existing loki clients
* embedable: other go applications should be able to easily import and run the Loki Lite server as a normal http handler

## Other
Project should be started by forking git@github.com:abferm/go-dev.git for project setup including development container.