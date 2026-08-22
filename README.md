# homepage

The page behind [jchevertonwynne.uk](https://jchevertonwynne.uk). It counts
visitors and draws each one a picture.

- Go + `net/http`, no web framework
- **No dependencies at all** — the image is drawn with the standard library's
  `image` package, so `go.mod` has an empty require block and the Pi
  cross-compile needs no toolchain
- No JavaScript
- Runs on a Raspberry Pi, exposed through a Cloudflare tunnel

Every visit to `/` increments a counter. That number is zero-padded to six
digits and drawn as a seven-segment odometer, composited over a field of
translucent circles whose colours and positions are seeded by the same number.
The picture for visit 1,337 always looks the same, and looks nothing like the
one for 1,338.

## How the pieces fit

**`internal/counter`** keeps the count in memory and writes it to disk on a
ticker, plus once more on SIGTERM. Writing on every request would be one SD
card write per crawler hit, which is a lot of wear for a number nobody reads to
the second. Because systemd sends SIGTERM on restart and shutdown, a deploy or
a reboot loses nothing; only a power cut can, and at most one tick's worth.

The write itself goes to a temp file, is fsynced, renamed over the target, and
then the containing directory is fsynced too — a rename is atomic but its
directory entry is not durable until the directory is flushed. The file holds a
plain decimal number, so `cat count.txt` works.

An unreadable count file is a startup error rather than a silent reset to zero.
If something is there and we cannot parse it, that is worth a human looking at.

**`internal/art`** is deterministic: the same count always produces the same
bytes. That is what makes the immutable cache header on `/image/{n}.png`
truthful rather than a promise that breaks at the next deploy.

Note for anyone editing it: `image/draw` treats `color.RGBA` as
**alpha-premultiplied**. Writing `color.RGBA{120, 255, 170, 26}` for a dim
green is not dim green — the channels exceed the alpha, which is not a valid
premultiplied colour, and it composites to magenta. Every translucent colour
goes through the `rgba` helper for that reason, and a test asserts it.

## Routes

| Route | |
|---|---|
| `GET /` | The page. Increments the counter. `Cache-Control: no-store` |
| `GET /image/{n}.png` | The picture for count `n`. Immutable, cached for a year |
| `GET /healthz` | Liveness. Does **not** increment |

Putting the count in the image path is what makes the expensive half of the
work cacheable — Cloudflare serves repeat hits from its edge, so the Pi renders
each image at most once.

## Public, deliberately

Unlike `weight-tracker` on the same Pi, nothing sits in front of this. No
Cloudflare Access, no login. Anyone can load it, which means:

- The canvas size is a **fixed constant**, never a query parameter. A
  caller-chosen width and height would let anyone ask a Raspberry Pi to
  allocate and fill an arbitrarily large buffer.
- `{n}` is parsed with `ParseUint` and rejected above six digits.
- Nothing reachable over HTTP writes anything but the counter.
- The count *will* be inflated by crawlers. That is what a public hit counter
  is; it is not measuring anything important.

If you fork this, do not add a route that takes a size, a filename, or a
redirect target without thinking about who can call it.

## Run locally

```sh
make run          # :8080
make check        # gofmt, vet, race tests — what CI runs
```

## Deploy

Same shape as `weight-tracker`: cross-compile, upload, restart the unit.

```sh
make build-pi     # GOOS=linux GOARCH=arm64
make deploy       # upload + restart on PI_HOST
make deploy-tunnel
```

Pushing to `main` does the same thing automatically via `.github/workflows/ci.yml`.

### One-time setup on the Pi

1. Install the unit from `deploy/homepage.service` to `/etc/systemd/system/`,
   then `sudo systemctl enable --now homepage`. It declares
   `StateDirectory=homepage`, so systemd creates and owns `/var/lib/homepage`
   for the count file — deliberately not next to the binary, so replacing the
   binary on every deploy never touches it.
2. Create the tunnel and route the apex:
   ```sh
   cloudflared tunnel create homepage
   cloudflared tunnel route dns homepage jchevertonwynne.uk
   ```
   Cloudflare flattens the CNAME at the zone root, so an apex hostname works.
   Then `make deploy-tunnel` to install the config and unit.
3. **A dedicated deploy key.** The existing key on this Pi is restricted by a
   forced-command wrapper to exactly the two commands `weight-tracker`'s deploy
   issues, so it cannot deploy this. Generate a second keypair with its own
   wrapper allowing only `cat > ~/homepage-new` and the homepage
   stop/replace/start sequence — a second key rather than widening the first,
   so a leaked homepage key cannot touch weight-tracker.
4. Repository secrets: `TAILSCALE_AUTHKEY` (the existing `tag:ci` key is
   already scoped to the Pi's `:22` and can be reused) and `PI_DEPLOY_SSH_KEY`
   set to the new key's private half.

The tunnel deliberately uses its own name, config path
(`/etc/cloudflared/homepage.yml`) and unit (`cloudflared-homepage.service`), so
it cannot collide with the weight-tracker tunnel already running on this Pi.

## Backups

The count is one small file:

```sh
scp jcw@jcwpi:/var/lib/homepage/count.txt .
```

Restoring is writing a number into it while the service is stopped. There is
nothing else to keep.
