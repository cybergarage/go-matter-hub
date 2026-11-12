# go-matter-hub

A Matter hub built on [go-matter](https://github.com/cybergarage/go-matter): a controller which stays up, with a web UI and an HTTP API.

One executable holds the daemon and the web UI. It is meant to run on a host on the home network, and to be used from a browser on another machine.

## Status

The hub keeps a fabric of its own, connects to the nodes commissioned into it, follows their state by subscription, and shows the devices and their state, live, in the web UI and over the API (phase B3). There is no way yet to commission a device from the hub or to control one.

| Phase | Scope | Status |
| --- | --- | --- |
| B1 | Daemon, embedded Svelte UI, access control, CI | Done |
| B2 | A fabric of the hub's own; connect to the commissioned nodes at startup and keep their state by subscription | Done |
| B3 | Read API (nodes, state) and live updates over Server-Sent Events; device list | Done |
| B4 | Control API and UI: on/off, level, color | Planned |
| B5 | Commissioning from the UI | Planned |
| B6 | systemd unit, configuration file, key store | Planned |

Automation and remote access are out of scope for now.

## Build and run

Go 1.25 or later, and Node.js 22 or later with npm. The hub depends on go-matter v0.9.0.

```sh
make build        # builds web/dist, then bin/go-matter-hub
bin/go-matter-hub # http://127.0.0.1:8580
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-listen` | `127.0.0.1:8580` | Address to listen on |
| `-data-dir` | `~/.go-matter-hub` | Directory for the hub's fabric, its commissioned nodes and the access token |
| `-vendor-id` | `0xFFF1` | Administrator vendor ID of the fabric the hub creates at its first start |
| `-log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `-allowed-host` | | Host name by which browsers may reach the hub; repeatable |
| `-require-auth` | off | Require the access token on the loopback interface too |
| `-version` | | Print the version |

## Fabric and nodes

The hub is a Matter controller with a fabric of its own. It does not use the store of `matterctl` or of another go-matter application, so a device commissioned with those is not a device of the hub.

- **First start.** The hub creates its fabric in the data directory (`fabric.json`) and logs its ID. Later starts use that fabric; it is never replaced. A data directory which holds commissioned nodes but no fabric is refused.
- **Nodes.** At every start the hub connects to each node commissioned into its fabric (`commissions/` in the data directory) and subscribes to all its attributes. It keeps the last value of every attribute in memory.
- **Status.** A node is `connecting` until its first report, `online` while reports arrive, and `offline` when it cannot be reached or its subscription is lost. The hub keeps trying, waiting from 2 seconds up to 2 minutes between attempts, and keeps the last values meanwhile.
- **Log.** A change of status and every changed attribute is logged:

```
level=INFO msg="node status" node=0000000016A5E84A status=online
level=INFO msg="attribute changed" node=0000000016A5E84A endpoint=1 cluster=0x0006 attribute=0x0000 value=true
```

`fabric.json` holds the private keys of the fabric, unencrypted, readable by the owner only (mode 0600, in a directory of mode 0700). Whoever can read it can control every device of the hub. A key store is planned (B6).

Until the hub can commission (B5), its data directory can be given a device with go-matter's `matterctl`, pointed at the same directory while the hub is stopped:

```sh
matterctl tui --store-dir ~/.go-matter-hub
```

## Access control

By default the hub listens on the loopback interface only, and needs no sign-in: only programs on the same host can reach it.

To use it from other machines, listen on a network address:

```sh
bin/go-matter-hub -listen 0.0.0.0:8580
```

The hub then always requires its **access token**. This cannot be turned off.

- The token is created at the first start, 256 random bits, in `<data-dir>/token` with mode 0600. The hub logs the path of the file, never the token. A token file other users can read is refused.
- **Browser**: enter the token once on the sign-in page. The hub sets a session cookie (`HttpOnly`, `SameSite=Strict`) valid for 30 days. Sessions are kept in memory, so restarting the hub signs every browser out.
- **Scripts**: send `Authorization: Bearer <token>`.
- After 10 wrong tokens within a minute, sign-in is refused for the rest of that minute.
- To replace the token, delete the file and restart the hub.

```sh
curl -H "Authorization: Bearer $(cat ~/.go-matter-hub/token)" http://192.168.1.20:8580/api/v1/hub
```

Two checks apply whether or not the token is required. They stop a web page on another site from using the browser of someone on the home network to reach the hub:

- **Host header.** A request has to address the hub by an IP address, by `localhost`, by the host name of the machine (also with `.local`), or by a name given with `-allowed-host`. Other names are refused, which defeats DNS rebinding.
- **Origin.** A request which changes something has to come from a page the hub itself served, and a request body has to be JSON.

### What this does not cover

The hub speaks plain HTTP. On the network, the token (at sign-in and in a bearer header) and the session cookie are not encrypted, so anyone who can capture traffic between the browser and the hub can read them. Use it on a network you trust, or put it behind a TLS reverse proxy. TLS in the hub itself is not implemented.

## API

All paths are under `/api/v1`. Errors are `{"error": {"code": "...", "message": "..."}}`.

| Method and path | Access | Result |
| --- | --- | --- |
| `GET /health` | Public | `{"status": "ok", "version": "..."}` |
| `GET /session` | Public | `{"authRequired": bool, "authenticated": bool}` |
| `POST /session` | Public | Body `{"token": "..."}`; starts a browser session |
| `DELETE /session` | Public | Ends the browser session |
| `GET /nodes` | Token | `{"nodes": [node, ...]}`, ordered by node ID |
| `GET /nodes/{id}` | Token | One node |
| `GET /events` | Token | The state of the nodes as Server-Sent Events; see below |
| `GET /hub` | Token | `{"version": "...", "authRequired": bool, "fabricId": "<16 hex digits>", "nodes": {"total": n, "online": n}}` |

A node:

```json
{
  "id": "0000000016A5E84A",
  "vendorId": 4631,
  "productId": 1,
  "commissionedAt": "2026-11-12T09:00:00Z",
  "status": "online",
  "lastReportAt": "2026-11-12T14:20:31.2Z",
  "attributes": [
    {"endpoint": 1, "cluster": 6, "attribute": 0, "value": true, "updatedAt": "2026-11-12T14:20:31.2Z"}
  ]
}
```

- `id` is 16 hexadecimal digits, because a JSON number cannot hold a 64-bit ID exactly.
- `status` is `connecting`, `online` or `offline`; `error` says why a node is offline.
- `attributes` are the last values the node reported, by endpoint, cluster and attribute ID. A value is what the node sent, without the types of the data model applied: a boolean, a number, a string, `null`, an array for a list, and an object keyed by field number for a structure. An integer beyond 2^53 is a string of decimal digits, and an octet string a string of hexadecimal digits. An attribute the node refused to report has `status`, the Interaction Model status code, instead of a value.

### Events

`GET /events` is a `text/event-stream`. The session cookie authenticates a browser's `EventSource`; a script sends the bearer token.

| Event | Data | Meaning |
| --- | --- | --- |
| `nodes` | `{"nodes": [node, ...]}` | Sent first on every connection: all the nodes |
| `status` | `{"node": id, "status": "...", "error": "..."}` | A node's status changed |
| `attributes` | `{"node": id, "priming": bool, "attributes": [...]}` | Attributes changed. With `priming` true they are the whole state of the node and replace what the client holds |

A comment line is sent every 15 seconds on an idle stream. A client which falls 256 events behind is disconnected; it connects again and starts from a fresh `nodes` event.

```sh
curl -N http://127.0.0.1:8580/api/v1/events
```

## Development

```sh
make test       # gofmt, go vet, go test -race
go test -short ./...  # without the test which needs mDNS on the network
make check-web  # svelte-check

# UI with hot reload on http://localhost:5173, against a hub on its default address
go run ./cmd/go-matter-hub &
cd web && npm install && npm run dev
```

The Go binary embeds `web/dist`. Without a built UI the hub still builds and serves the API; the UI path answers with a notice.

### Developing against a local go-matter

Check this repository out next to go-matter and add it to the Go workspace of the parent directory, so that a build here uses the go-matter sources beside it:

```sh
cd .. && go work use ./go-matter-hub
```

CI and releases do not use the workspace: they build against the go-matter version in `go.mod`. Before opening a pull request, check that the hub also builds that way:

```sh
GOWORK=off go build ./... && GOWORK=off go test ./...
```

## Layout

| Path | Role |
| --- | --- |
| `cmd/go-matter-hub` | The daemon: flags, listener, shutdown |
| `internal/hub` | The hub: its fabric, the sessions and subscriptions with the nodes, their state |
| `internal/auth` | Access token file, bearer and session authentication |
| `internal/server` | HTTP API, event stream, Host and Origin checks, web UI serving |
| `web` | Svelte + Vite + TypeScript UI; `web/dist` is embedded |

## License

Apache License 2.0. See [LICENSE](LICENSE).
