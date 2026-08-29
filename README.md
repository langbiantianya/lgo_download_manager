# ldm — Local Download Manager

A single-binary download manager with a Fyne GUI, persistent SQLite state,
concurrent chunked transfers, and a `lgom://` URL scheme for browser
hand-offs.

## Features

- Concurrent range-based downloads (HTTP, HTTPS, FTP, WebDAV).
- Resumable: pause/restart picks up at byte offsets, never re-downloads
  completed chunks.
- Per-task settings (URL, save path, threads, chunk size, UA, cookies,
  FTP mode).
- `lgom://download?url=...&name=...&ua=...&headers=...&cookies=...` URL
  scheme — register it as a desktop handler to send URLs from the
  browser into the running app via a Unix socket.
- Persistent task list and configuration in SQLite (`ldm.sqlite`).
- Live progress, per-chunk bars, speed, ETA in the GUI.
- Single-instance lock: a second launch forwards URLs to the primary.

## Build

```sh
go build -o ldm .
```

Run with the GUI:

```sh
./ldm
```

Run headless (scheduler only, no Fyne window):

```sh
./ldm -no-gui
```

## CLI flags

| Flag      | Default        | Description                       |
| --------- | -------------- | --------------------------------- |
| `-db`     | `ldm.sqlite`   | SQLite database path              |
| `-no-gui` | `false`        | Start without the Fyne GUI        |
| `-open-url` | `""`         | A `lgom://...` URL to enqueue     |

Positional arguments are also accepted as `lgom://` URLs (some desktop
environments pass the URL positionally).

## URL scheme

`lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]`

Only `url` is required. If the app is not running, the second launch
forwards the URL to the primary instance and exits.

## Settings

Stored in the `settings` table of `ldm.sqlite` and applied to new
downloads. Edit via the **设置** page in the GUI.

| Field              | Default            | Notes                                       |
| ------------------ | ------------------ | ------------------------------------------- |
| Default save dir   | `~/Downloads`      | Used by "新建任务"                           |
| Default threads    | 16                 | Per-task max concurrent connections         |
| Min chunk size     | 1 MiB              | Engine floor for chunk sizing               |
| User-Agent         | `Wget/1.21.3`      | Sent on every request                       |
| Cookies            | ""                 | Cookie header string                        |
| FTP passive mode   | true               | Passive vs active FTP                        |
| Disk preallocation | true              | Pre-allocate the file to its full size      |

Changes are persisted on every edit.

## Per-task state

| Field            | Persisted | Notes                                              |
| ---------------- | --------- | -------------------------------------------------- |
| Chunk ranges     | yes       | Engine's actual layout — used by the chunk UI      |
| Chunk progress   | yes       | Offset-from-start for each chunk                   |
| Total / downloaded | yes     | Cumulative byte counters                            |
| Status           | yes       | `Pending` / `Downloading` / `Paused` / `Completed` / `Failed` |

The `chunk_ranges` column was added later; older tasks fall back to a
per-thread even-split on first render and get the real layout after
the next Start.

## Architecture

```
main.go                        # flags, single-instance lock, GUI launch
internal/store/                # SQLite persistence (tasks, settings, chunk state)
internal/protocol/             # HTTP, HTTPS, FTP, WebDAV drivers
internal/engine/               # chunk planning, byte-range downloads, retry
internal/scheduler/            # per-task lifecycle, status events, progress flush
internal/prealloc/             # disk preallocation helper
internal/urllauncher/          # lgom:// URL parsing, Unix socket forwarding
internal/ui/                   # Fyne window, task list, settings dialog, chunk bars
```

The engine plans chunks by `Min(MinChunkSize, ceil(total / MinChunkSize))`,
capped by `ChunkCount`. Servers that don't advertise `Accept-Ranges`
fall back to a single-stream download with a one-chunk plan.

## Testing

```sh
go test ./...
```

The most informative test is
`internal/ui/chunk_details_e2e_test.go` — it runs the real scheduler
plus engine through a download against an httptest server without
`Accept-Ranges` (forcing the streaming fallback), waits for completion,
and asserts every per-byte-range bar reaches `Value = 1.0`.

## License

Private / unspecified.