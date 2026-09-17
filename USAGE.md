# dps usage

- [Install options](#install-options)
- [First run](#first-run)
- [Usage](#usage)
- [Interactive keys](#interactive-keys)
- [Columns](#columns)
- [Configuration](#configuration)
- [How dps finds Docker](#how-dps-finds-docker)
- [Output contract](#output-contract)
- [Development](#development)
- [Limitations](#limitations)

## Install options

The install script first says whether this machine can run `dps` at all, and
stops before downloading anything when it cannot:

```
dps: checking requirements

  [v] os        darwin
  [v] arch      arm64
  [v] download  curl
  [v] checksum  sha256sum
  [v] tar       /usr/bin/tar
  [x] docker    docker is installed but no daemon is running
  [v] install   /usr/local/bin is writable

dps: requirements not met — fix the lines marked [x] and run this again
```

Every line is checked and printed, not only the first one that fails — two
missing tools are learned in one run. A Docker daemon that does not answer is a
failing line: `dps` reads containers from it, so a machine without one has
nothing to install for. `DPS_SKIP_CHECKS=1` installs anyway.

Once the checks pass it downloads the release built for that platform, checks it
against the published `checksums.txt`, installs one file, and runs `dps` once.
On a machine that has never configured `dps`, that first run is the
[setup](#first-run).

It never asks for a password. It installs in `/usr/local/bin` when it can write
there — as root, or through a `sudo` that needs no password — and in
`~/.local/bin` otherwise. When that directory is not on your `PATH`, the closing
message is the one line that fixes it.

Read it before you run it if you prefer — it is one file of POSIX `sh`:

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh | less
```

### Variables

| Variable | Effect |
|---|---|
| `DPS_VERSION` | Install this tag instead of the latest, e.g. `v0.2.0`. |
| `DPS_INSTALL_DIR` | Install into this directory instead of the default one. |
| `DPS_NO_SUDO` | `1` never uses sudo. The binary goes to `~/.local/bin`. |
| `DPS_SKIP_CHECKS` | `1` installs without the requirement checklist. |
| `DPS_NO_RUN` | `1` installs without running `dps` afterwards. |

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dps/main/install.sh |
  DPS_INSTALL_DIR="$HOME/bin" DPS_VERSION=v0.2.0 sh
```

### By hand

Every release carries `linux/amd64`, `linux/arm64`, `darwin/amd64` and
`darwin/arm64` as `.tar.gz`, plus a `checksums.txt`, on the
[releases page](https://github.com/developerabdan/dps/releases) — download one
by hand if you would rather not pipe a script into a shell.

### From source

```sh
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

## First run

The first `dps` on a machine with no config file opens a short setup, then
exits to the shell. Two questions:

1. **Which columns to show.** The full catalog, with `name`, `state`, `image`
   and `ports` already ticked. `space` toggles, `enter` accepts.
2. **Grouped or flat.** An example table redraws under the choice as you move
   between the two, so the difference is read rather than described.

It ends by naming what it saved and where. Nothing it asks is a gate — every
answer is also a flag — and the file is written only when the last question is
answered. Quitting early writes nothing and leaves the setup to open again.

The setup needs a terminal on stdin and stdout. `dps` in a pipe, a script or a
cron job never stops to ask: it uses the defaults and prints the table.

```sh
dps --onboard    # run the setup again, starting from your current settings
```

## Usage

```
dps [flags]
```

On a terminal you get the interactive view: colour, live refresh, and a cursor
you can move. Through a pipe you get a plain table with no colour and no
headings, so `dps | grep exited` and `dps | awk '{print $1}'` behave the way
every other unix tool does. Nothing about that is a flag — it follows stdout.

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
| `-onboard` | Run the first-run setup again, then exit. |
| `-pick-cols` | Tick your default columns from a list, then exit. |
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
| `s` | Open the stats view for the selected container |
| `e` | Open a shell in the selected container, after you answer `y` |
| `c` | Choose the columns |
| `a` | Toggle stopped containers |
| `r` | Refresh now, and say what changed |
| `q`, `esc`, `ctrl+c` | Quit |

The list scrolls inside the window — it never asks for a taller terminal. While
the list is longer than the window the status line says which row you are on,
for example `row 12/37`. A cursor on the first row of a project keeps that
project's heading on screen.

The view refreshes every two seconds. A row that is new, or whose state,
health, image or ports changed since the last refresh, is drawn bold for a
moment. Uptime text alone does not count — it changes every minute by itself.
`r` refreshes at once and puts the result on the status line, for example
`↻ refreshed · 1 changed, 1 gone`, so a refresh that found nothing still shows
that it ran.

### Shell

`e` asks first, on the status line:

```
open a shell in plane-app-api-1? y/n
```

`y` or `enter` opens the shell. Any other key cancels and does nothing else, so
an `e` typed by accident costs one more key and no more.

The shell opens in the same terminal. It runs `bash` when the image has it and
`sh` when it does not, so there is nothing to choose. Type `exit` or press
`ctrl+d` to come back to the list, which is read again straight away.

A stopped container has no shell to open, and an image with no `/bin/sh` —
distroless, `scratch` — has none to run. In both cases the status line says so
and the list stays open.

### Stats

`s` opens the stats view for the selected container. It fills the window with
four charts, updated every two seconds:

| Chart | Shows |
|---|---|
| CPU | Share of one CPU, as `docker stats` prints it, so two full cores read 200% |
| MEMORY | Memory in use, of the limit, without the page cache the kernel can take back |
| DISK | Read and write rates side by side, with the totals |
| NETWORK | Received (`in`) and sent (`out`) rates side by side, with the totals |

The right end of each label gives the scale, for example `0–46%`. The top of a
chart is a little above the highest value on it, but never less than a floor
(10% CPU, 16MB memory, 100kB/s disk, 10kB/s network), so an idle container
draws a flat line and not its own noise at full height.

`↑` `↓` move to the next running container without leaving the view. `esc`,
`q` or `s` go back to the list. The charts start empty and fill from the right;
the history starts when the view first samples that container and is not kept
after dps exits.

### Choosing columns

`c` opens a list of every column above the table. `space` ticks or unticks the
one under the cursor, and the table below changes at once, with your real
containers. `enter` saves the result as your default; `esc` puts the columns
back as they were.

The same list, without the table, runs from the shell:

```sh
dps --pick-cols
```

Columns you had keep their order. A column you tick goes in at its place in the
catalog.

## Columns

`dps --list-cols` prints this catalog:

| Key | Header | Shows |
|---|---|---|
| `name` | NAME | Container name, with the project prefix removed |
| `state` | STATE | Running state and uptime, e.g. `● up 3h` |
| `image` | IMAGE | Image, with the registry stripped |
| `ports` | PORTS | Published ports, compacted, e.g. `8090→80` |
| `cpu` | CPU | CPU graph of the last 20 seconds and the latest share, e.g. `▁▂▅▇▅▃▂▁▁▂  12.4%` |
| `health` | HEALTH | Healthcheck result |
| `created` | CREATED | Age |
| `project` | PROJECT | Compose project |
| `service` | SERVICE | Compose service |
| `ip` | IP | Container IP |
| `size` | SIZE | Writable layer size (costs an extra query) |
| `id` | ID | Short container ID |
| `cmd` | COMMAND | Entrypoint command |

The default set is `name,state,image,ports`.

`cpu` works in the interactive view only, because a graph needs a history and
only the interactive view keeps one. The plain table and `-w` leave it out. When
`--cols` or `--preset` asked for it, stderr says so; when it comes from your
saved default, the table is quiet, so `dps | grep` does not warn on every run.
The column samples each running container once every two seconds, and dps
samples nothing when the column is not in the set. Its highest bar stops short
of the full cell, so the graphs on two rows next to each other never touch.

When the terminal is too narrow for every column, `dps` drops the lowest
priority ones — but it says so on stderr rather than hiding data quietly:

```
dps: 2 columns hidden (created, cmd) — widen the window or use --wide
```

Because that notice goes to stderr, it stays out of `dps | awk` while still
being said out loud.

## Configuration

Optional. The file is plain JSON, and `--set-cols`, `--save-preset`,
`--pick-cols` and the `c` key only write what you could have typed yourself.

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

`dps` asks for Docker API 1.44 and then clamps to whatever the daemon reports it
supports, so old and new engines both work. It reads `GET /version` and
`GET /containers/json`. The one thing it ever writes is the shell that `e`
opens, through the exec endpoints.

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

Go 1.25 or newer. The one runtime dependency is Bubble Tea, for the interactive
view.

```sh
go build ./...
go test ./...
gofmt -l .
```

## Limitations

- Unix sockets only. No `tcp://`, no `ssh://`. To read a remote host, ssh in and
  run `dps` there.
- It does not start, stop or remove containers. The only thing it runs inside
  one is the shell that `e` opens.
- Stats history lives in memory. It starts when dps opens and is gone when dps
  exits.
- The `sort`, `strip_registry` and `strip_project_prefix` config keys are not
  implemented yet.
