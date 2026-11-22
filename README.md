# push-client

A long-running client for the [push](https://github.com/4levity/push)
status service.

- **Single static Go binary**, run as a systemd **service** (no timer, no
  cron, no curl, no python3). It sends this host's status **every
  interval** (default 5m); the first push goes out at service start.
- The payload is the **base status file** with **extra status files**
  merged over it (shallow merge, later files win). A missing or broken
  extra file never stops the heartbeat: its **last good copy**, cached on
  this host, is merged instead, and the miss is logged to the journal.
- One line-based **config file** (endpoint, name, token, interval, status
  files), reloaded on **SIGHUP**.
- Built for the hosts of the push service; the companion ansible role
  installs the binary, the config and the unit (see
  [systemd/push-client.service](systemd/push-client.service) for the
  reference unit).

## How it works

```
this host ──HTTP:443──▶ reverse proxy (TLS) ──▶ push service client API
   every interval: POST /{name}, X-Client-Token, JSON payload
```

- The first push goes out at service start (the network may not be up
  yet; the next tick recovers), then every interval.
- One attempt per tick. A failure is logged to the journal and the next
  tick is the retry; the schedule never drifts into a retry storm.
- The service rate-limits clients to 60 requests/minute per IP, so the
  default 5m interval (minimum accepted: 1s) has room to spare.

## Configuration

Line-based file, one `key=value` per line, `#` comments and blank lines
allowed. See [config.example.conf](config.example.conf).

| key | required | default | meaning |
|---|---|---|---|
| `url` | yes | – | the service endpoint, `http(s)://host[:port][/path]`, no trailing slash |
| `name` | yes | – | this client's name (letters/numbers/dashes; must match the service's client list) |
| `token` | yes | – | the raw client token (the service stores only its SHA-256) |
| `interval` | no | `5m` | push period (Go duration; minimum 1s) |
| `status` | no | – | base status file (absolute path, a JSON object) |
| `status_file` | no | – | extra status file (absolute path, a JSON object); **repeatable**, merged in file order |
| `cache` | no | – | directory for the last good copies of the `status_file` entries |

The file is read at startup and on **SIGHUP** (a failed reload keeps the
previous configuration). `status_file` may repeat; every other key may
not. Unknown keys, malformed lines, duplicate keys, and relative paths
are rejected: check a config with `push-client validate`.

### Status payload

The payload is assembled fresh on every tick:

1. the `status` file is the base; missing or broken, the base is `{}` and
   the miss is logged;
2. each `status_file` is merged over the base in file order (shallow:
   top-level keys, later files win);
3. a `status_file` that is missing, unreadable, or not a valid JSON
   object falls back to its last good copy in `cache` (when one exists),
   and the miss is logged; with no copy it is skipped;
4. a successfully read `status_file` updates its cache copy (atomic
   rename).

The heartbeat goes out in every case — worst case it is `{}` — and the
journal carries what went wrong. The cache layout is the one the bash
reference client used (the file path with every run of non-alphanumerics
replaced by `_`), so an existing `/var/lib/push-client` carries over.

## Running

```sh
push-client run       [-config /etc/push-client/conf] [-log-format json|text]
push-client validate  [-config /etc/push-client/conf]
```

Under systemd (see [systemd/](systemd/push-client.service)):

```sh
systemctl reload push-client   # SIGHUP: re-read the config
journalctl -u push-client -f
```

Each push is logged (`status pushed`, or `status push failed` with the
error), so the journal shows the heartbeat and every miss. A fatal
config error at startup exits non-zero and `Restart=on-failure` retries.

## Building and testing

Requires Go ≥ 1.23. There are no dependencies.

```sh
make build        # static binary at build/push-client
make test         # go test -race ./...
make lint         # golangci-lint (installs v2.15.0 to GOPATH/bin if missing)

./build/push-client validate --config config.example.conf
./build/push-client run --config config.example.conf
```

Tag a `v*` to publish a static `push-client_linux_{amd64,arm64}` GitHub
release (the ansible role installs one of these binaries; no Go toolchain
needed on the target).

## Security notes

- The config file holds the **raw token**: install it `0600`, owner
  root (or the service user). The push service stores only the token's
  SHA-256.
- `run` and reload warn to the journal when the config file is
  group/world readable.
- The token never appears in logs.
- The cache holds last good copies of status files: it is created `0700`
  and the copies `0600`.
- Client names and tokens cannot be probed through the service (the
  client API never reveals registration; see the push service's security
  notes).
- The client sends only to the configured endpoint over TLS (the proxy
  terminates it); the endpoint URL is validated to http/https.

## Project layout

```
main.go                     CLI: run / validate
internal/app                run loop (schedule, SIGHUP reload)
internal/config             line-based config load + validation
internal/collector          payload assembly: base + extras + last-good cache
internal/push               HTTP POST of the payload
systemd/                    reference systemd unit
config.example.conf         commented config example
```
