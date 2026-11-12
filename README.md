# go-matter-hub

A Matter hub built on [go-matter](https://github.com/cybergarage/go-matter): a controller which stays up, with a web UI and an HTTP API.

One executable holds the daemon and the web UI. It is meant to run on a host on the home network, and to be used from a browser on another machine.

## Status

This is the skeleton (phase B1): the daemon, the embedded web UI and the access control of the API. It does not talk to Matter devices yet.

| Phase | Scope | Status |
| --- | --- | --- |
| B1 | Daemon, embedded Svelte UI, access control, CI | Done |
| B2 | Connect to the commissioned nodes at startup and keep their state by subscription | Planned |
| B3 | Read API (nodes, state) and live updates over Server-Sent Events; device list | Planned |
| B4 | Control API and UI: on/off, level, color | Planned |
| B5 | Commissioning from the UI | Planned |
| B6 | systemd unit, configuration file, key store | Planned |

Automation and remote access are out of scope for now.

## Build and run

Go 1.25 or later, and Node.js 22 or later with npm.

```sh
make build        # builds web/dist, then bin/go-matter-hub
bin/go-matter-hub # http://127.0.0.1:8580
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-listen` | `127.0.0.1:8580` | Address to listen on |
| `-data-dir` | `~/.go-matter-hub` | Directory for the access token and the hub's data |
| `-allowed-host` | | Host name by which browsers may reach the hub; repeatable |
| `-require-auth` | off | Require the access token on the loopback interface too |
| `-version` | | Print the version |

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
| `GET /hub` | Token | `{"version": "...", "authRequired": bool}` |

## Development

```sh
make test       # gofmt, go vet, go test -race
make check-web  # svelte-check

# UI with hot reload on http://localhost:5173, against a hub on its default address
go run ./cmd/go-matter-hub &
cd web && npm install && npm run dev
```

The Go binary embeds `web/dist`. Without a built UI the hub still builds and serves the API; the UI path answers with a notice.

To develop against a local go-matter checkout once the hub depends on it, create a `go.work` (ignored by git):

```sh
go work init . ../go-matter
```

## Layout

| Path | Role |
| --- | --- |
| `cmd/go-matter-hub` | The daemon: flags, listener, shutdown |
| `internal/auth` | Access token file, bearer and session authentication |
| `internal/server` | HTTP API, Host and Origin checks, web UI serving |
| `web` | Svelte + Vite + TypeScript UI; `web/dist` is embedded |

## License

Apache License 2.0. See [LICENSE](LICENSE).
