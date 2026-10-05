# HTTP API and dashboard — LLD

**Code:** `internal/transport/httpapi/`, embedded assets in `web/`.

The HTTP adapter turns JSON requests into control-plane calls. It does not
schedule or execute jobs. `server.go` registers method-aware routes with a
private `http.ServeMux`; `ui.go` serves the embedded dashboard; `/metrics`
exports the control-plane registry.

| Area | Routes | Behaviour |
| --- | --- | --- |
| Functions | `/v1/functions`, `/{id}/artifacts`, `/{id}/versions`, `/{id}/jobs` | Upload archive, create version, invoke |
| Jobs | `/v1/jobs`, `/{id}`, `/{id}/result`, `/{id}/cancel` | Submit and inspect lifecycle |
| Diagnostics | `/{id}/logs`, `/{id}/events`, `/v1/jobs/count`, `/v1/nodes` | Read execution and node state |
| UI / metrics | `/`, `/jobs/{id}`, `/assets/*`, `/metrics` | Browser views and Prometheus scrape |

Submission decodes one JSON object with unknown fields rejected. The job
list's `limit` parameter is optional; the dashboard requests a recent slice
and fetches the total count separately. The result endpoint returns 202 while
no result exists. Transport errors map to HTTP status codes, while lifecycle
rules remain in `controlplane` and `job`.

The UI polls HTTP; it is not a live event stream. There is no auth, rate
limiting, or pagination cursor. Generated sqlc models currently shape much of
the public JSON, so a database schema change can alter responses unless the
read path is explicitly stabilized.
