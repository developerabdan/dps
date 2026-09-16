# dps

A readable `docker ps`.

`docker ps` prints one very wide line per container, wraps it, and leaves you to
find the part you care about. `dps` fits the table to the terminal it is running
in, groups containers by their compose project, and scrolls when the list is
longer than the window.

```
NAME           STATE    IMAGE                           PORTS
plane-app  12/12 up
  proxy        ● up 3h  plane-proxy:v1.3.1              8090→80
  admin        ● up 3h  plane-admin:v1.3.1              —
  space        ● up 3h  plane-space:v1.3.1              —
  live         ● up 3h  plane-live:v1.3.1               —
  web          ● up 3h  plane-frontend:v1.3.1           —
  worker       ● up 3h  plane-backend:v1.3.1            —
  api          ● up 3h  plane-backend:v1.3.1            —
  plane-minio  ● up 3h  minio:latest                    —
  plane-db     ● up 3h  postgres:15.7-alpine            —
  plane-mq     ● up 3h  …itmq:3.13.6-management-alpine  —
  plane-redis  ● up 3h  valkey:7.2.11-alpine            —
12 containers · project plane-app · 12 up
```

On a terminal you get the interactive view: colour, live refresh, and a cursor
you can move. Through a pipe you get a plain table with no colour and no
headings, so `dps | grep exited` and `dps | awk '{print $1}'` behave the way
every other unix tool does. Nothing about that is a flag — it follows stdout.

## Requirements

**To run:**

| Need | Detail |
|---|---|
| Docker Engine | Any daemon that answers `GET /version`. `dps` asks for API 1.44 and then clamps to whatever the daemon reports it supports, so old and new engines both work. |
| A unix socket | `dps` speaks to unix sockets only. `DOCKER_HOST=tcp://…` and `ssh://…` are refused with an error, not a silent fallback. |
| Read access to that socket | Your user must be in the `docker` group, or run as root. `dps` only ever reads — `GET /version` and `GET /containers/json`. It never starts, stops or removes anything. |
| A UTF-8 terminal | The table uses `●`, `→` and `…`. A latin-1 terminal shows mojibake instead. |

No daemon, no agent, no config file is needed. A machine that has never seen
`dps` before runs it with defaults.

**To build:** Go 1.25 or newer. That is the only build dependency; the one
runtime dependency is Bubble Tea, for the interactive view.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh | sh
```

That line is the whole install, on a laptop or on a server that has no Go. The
script reads `uname`, downloads the release built for that platform, checks it
against the published `checksums.txt`, and installs one file.

It installs in `/usr/local/bin`, so that `dps` works the moment the script ends.
That directory belongs to root, so `sudo` asks for your password once — for the
one `install` command and nothing else. Refuse the password, press ctrl-c, or
set `DPS_NO_SUDO=1`, and the binary goes to `~/.local/bin` instead; the script
then prints the line that puts that directory on your `PATH`.

Read it before you run it if you prefer — it is one file of POSIX `sh`:

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh | less
```

| Variable | Effect |
|---|---|
| `DPS_VERSION` | Install this tag instead of the latest, e.g. `v0.2.0`. |
| `DPS_INSTALL_DIR` | Install into this directory instead of the default one. |
| `DPS_NO_SUDO` | `1` never asks for a password. The binary goes to `~/.local/bin`. |

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh |
  DPS_INSTALL_DIR="$HOME/bin" DPS_VERSION=v0.2.0 sh
```

Supported targets: `linux/amd64`, `linux/arm64`, `darwin/amd64`,
`darwin/arm64`. Every release carries all four as `.tar.gz`, plus a
`checksums.txt`, on the [releases page](https://github.com/developerabdan/dps/releases)
— download one by hand if you would rather not pipe a script into a shell.

### Other ways

```sh
# with Go — the binary lands in $(go env GOPATH)/bin
go install github.com/developerabdan/dps/cmd/dps@latest

# from source
git clone https://github.com/developerabdan/dps.git
cd dps
go build -ldflags "-X main.version=$(git describe --tags --always)" -o bin/dps ./cmd/dps
sudo install -m 0755 bin/dps /usr/local/bin/dps
```

### Check the install

```sh
dps --version
dps
```

If the daemon cannot be reached, the error names the socket path it tried, so
you can see which one it resolved to.

### Upgrade and uninstall

Run the same `curl` line again to upgrade; it overwrites the binary in place.
To remove it, delete the one file: `sudo rm /usr/local/bin/dps`, and
`rm -rf ~/.config/dps` if you saved a column set.

## Usage

```
dps [flags]
```

| Flag | What it does |
|---|---|
| `-a` | Include stopped containers. |
| `-g`, `-group` | Group rows by compose project. On by default; `-g=false` turns it off. |
| `-f key=value` | Filter, passed to Docker. Repeatable: `-f status=running -f label=env=prod`. |
| `-cols LIST` | Columns for this run. `-cols name,ports` replaces the set; `-cols +health,-image` adjusts the saved one. |
| `-preset NAME` | Use a saved preset as the column set. |
| `-set-cols LIST` | Save these columns as your default, then exit. |
| `-save-preset NAME` | Save the resulting columns under a name, then exit. |
| `-w N` | Redraw every N seconds (plain table, no cursor). |
| `-wide` | No truncation and no dropped columns. Lines may run past the window. |
| `-plain` | Force the plain table even on a terminal. |
| `-json` | One JSON record per line. |
| `-config` | Print the config path and contents, then exit. |
| `-list-cols` | Print the column catalog, then exit. |
| `-version` | Print the version, then exit. |

### Examples

```sh
dps                          # grouped, interactive, live
dps -a                       # include stopped containers
dps -g=false                 # flat table
dps -f status=exited         # only what died
dps -cols +health,+created   # your usual columns, plus two
dps -preset debug            # a saved column set
dps -w 5                     # redraw every five seconds
dps --json | jq -r 'select(.state != "running") | .name'
dps | grep exited            # plain, greppable, no colour
```

## Interactive keys

The interactive view opens when stdout is a terminal and you passed none of
`-plain`, `-json` or `-w`.

| Key | Action |
|---|---|
| `↑` `↓`, `k` `j` | Move the cursor |
| `PgUp` `PgDn`, `ctrl+b` `ctrl+f`, `space` | Move a page |
| `ctrl+u` `ctrl+d` | Move half a page |
| `g` / `home`, `G` / `end` | First row, last row |
| wheel | Scroll the window |
| `a` | Toggle stopped containers |
| `r` | Refresh now |
| `q`, `esc`, `ctrl+c` | Quit |

The list scrolls inside the window — it never asks for a taller terminal. While
the list is longer than the window the status line says which row you are on,
for example `row 12/37`. A cursor on the first row of a project keeps that
project's heading on screen.

The view refreshes every two seconds.

## Columns

`dps --list-cols` prints this catalog:

| Key | Header | Shows |
|---|---|---|
| `name` | NAME | Container name, with the project prefix removed |
| `state` | STATE | Running state and uptime, e.g. `● up 3h` |
| `image` | IMAGE | Image, with the registry stripped |
| `ports` | PORTS | Published ports, compacted, e.g. `8090→80` |
| `health` | HEALTH | Healthcheck result |
| `created` | CREATED | Age |
| `project` | PROJECT | Compose project |
| `service` | SERVICE | Compose service |
| `ip` | IP | Container IP |
| `size` | SIZE | Writable layer size (costs an extra query) |
| `id` | ID | Short container ID |
| `cmd` | COMMAND | Entrypoint command |

The default set is `name,state,image,ports`.

When the terminal is too narrow for every column, `dps` drops the lowest
priority ones — but it says so on stderr rather than hiding data quietly:

```
dps: 2 columns hidden (created, cmd) — widen the window or use --wide
```

Because that notice goes to stderr, it stays out of `dps | awk` while still
being said out loud.

## Configuration

Optional. The file is plain JSON, and `--set-cols` and `--save-preset` only
write what you could have typed yourself.

Location: `~/.config/dps/config.json`, or `$XDG_CONFIG_HOME/dps/config.json`
when that variable is set. A missing file is not an error.

```json
{
  "cols": ["name", "state", "image", "ports"],
  "group_by_project": true,
  "presets": {
    "ports": ["name", "ports", "ip", "state"],
    "debug": ["name", "state", "health", "created"],
    "disk": ["name", "image", "size"]
  }
}
```

| Key | Effect |
|---|---|
| `cols` | Your default column set. |
| `group_by_project` | Default for `-g`. A `-g` typed on the command line wins, in both directions. |
| `presets` | Named column sets for `--preset`. |

`--set-cols` and `--save-preset` validate against the catalog before writing, so
a typo cannot save a file that later refuses to load. Writes go to a temporary
file and are renamed, so an interrupted save cannot leave a half-written config
behind.

A `sort` key is written into new config files but nothing reads it yet.

## How dps finds Docker

The same order the `docker` CLI uses:

1. `DOCKER_HOST`, when it is a `unix://` path.
2. The endpoint of the active Docker context, from `~/.docker/config.json` and
   `~/.docker/contexts/`.
3. `/var/run/docker.sock`.

Step 2 is why `dps` works on Docker Desktop for macOS, where the socket is at
`~/.docker/run/docker.sock` and `/var/run/docker.sock` does not exist.

To read a socket at a non-standard path:

```sh
DOCKER_HOST=unix:///run/user/1000/docker.sock dps
```

## Output contract

`--json` emits newline-delimited JSON, one container per line. The field names
are snake_case and stable — scripts depend on them:

```json
{"id":"0dec549e57b9…","name":"plane-app-proxy-1","image":"makeplane/plane-proxy:v1.3.1","state":"running","status":"Up 3 hours","ports":[{"ip":"127.0.0.1","public":8090,"private":80,"type":"tcp"}],"created":1782108483,"project":"plane-app","service":"proxy","ip":"172.24.0.7","command":"caddy run --config /etc/caddy/Caddyfile","restarts":0}
```

JSON carries the raw values, not the shortened ones the table prints: the full
ID, the full container name, and the image with its registry. Shortening is a
display decision, and a script should make its own.

Through a pipe, group headings are dropped. A script cannot tell a heading from
a container row, so `dps -g | awk` gets the flat table instead of something
quietly wrong.

## Development

```sh
go build ./...
go test ./...
gofmt -l .
```

## Limitations

- Unix sockets only. No `tcp://`, no `ssh://`. To read a remote host, ssh in and
  run `dps` there.
- Read-only. It shows containers; it does not stop, start or remove them.
- The `sort`, `strip_registry` and `strip_project_prefix` config keys are not
  implemented yet.

## License

MIT. See [LICENSE](LICENSE).
