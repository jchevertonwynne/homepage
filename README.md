# homepage

The page behind [jchevertonwynne.uk](https://jchevertonwynne.uk). It counts
visitors and draws each one a picture.

- Go + `net/http`, no web framework
- **No dependencies at all** — the image is drawn with the standard library's
  `image` package, so `go.mod` has an empty require block and the Pi
  cross-compile needs no toolchain
- No JavaScript
- Runs on a Raspberry Pi in a k3s cluster, exposed through a Cloudflare tunnel

Every visit to `/` increments a counter. That number is zero-padded to six
digits and drawn as a seven-segment odometer, composited over a field of
translucent circles whose colours and positions are seeded by the same number.
The picture for visit 1,337 always looks the same, and looks nothing like the
one for 1,338.

## How the pieces fit

**`internal/counter`** keeps the count in memory and writes it to disk on a
ticker, plus once more on SIGTERM. Writing on every request would be one SD
card write per crawler hit, which is a lot of wear for a number nobody reads to
the second. Kubernetes sends SIGTERM before SIGKILL, so a deploy or a reboot
loses nothing; only a power cut can, and at most one tick's worth.

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
| `GET /` | The page, with the picture embedded. Increments the counter. `Cache-Control: no-store` |
| `GET /healthz` | Liveness. Does **not** increment |

The picture is a `data:` URI in the page, not a URL. It used to be served from
`/image/{n}.png`, which was immutably cacheable and meant the Pi rendered each
image at most once — but it also meant anyone could walk `/image/1.png`
upwards and look at every visitor's picture, which makes "yours is the only one
that looks like that" untrue. There is now no address to walk.

That trade was measured rather than assumed. Inlining costs the only cacheable
thing the site had: every page load renders (~155ms on the Pi) instead of
Cloudflare serving a repeat, and the response grows from ~1.3KB to ~80KB. At
this traffic that is a few seconds of CPU an hour.

If the cost ever matters, the middle path is an unguessable URL —
`/image/179-<hmac>.png` signed with a server secret — which restores caching
while still making enumeration impossible.

Note for anyone editing the handler: the field is `template.URL`, not `string`.
`html/template` rejects `data:` URIs in `src` by default and silently
substitutes `#ZgotmplZ`, so the page would render with a broken image and
nothing in the logs. A test asserts it.

## Public, deliberately

Unlike `weight-tracker` on the same Pi, nothing sits in front of this. No
Cloudflare Access, no login. Anyone can load it, which means:

- The canvas size is a **fixed constant**, never a query parameter. A
  caller-chosen width and height would let anyone ask a Raspberry Pi to
  allocate and fill an arbitrarily large buffer.
- There is no image endpoint to abuse; the picture is embedded in the page.
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

## Deployment

Runs on the k3s cluster described in
[homelab](https://github.com/jchevertonwynne/homelab). Push to `main`: CI
builds an arm64 image, Flux notices the new tag, commits it to the homelab
repo and rolls the pod. Nothing here touches the Pi directly.

The visit count lives on a `hostPath` at `/var/lib/homepage`, which predates
the cluster; newer apps get a PersistentVolumeClaim instead. The Deployment
uses `Recreate` rather than `RollingUpdate` because the counter is
single-writer — two overlapping pods would each hold a count in memory and
the last to stop would win. The cost is a few seconds of 502 on every deploy,
which is the right trade for not losing visits.

`make build-pi` still cross-compiles a bare binary for testing on the Pi
directly, but it is not how this gets deployed.

## Backups

The count is one small file:

```sh
ssh jcw@jcwpi 'sudo cat /var/lib/homepage/count.txt'
```

Restoring is writing a number into that file while the Deployment is scaled
to zero. There is nothing else to keep.

```sh
kubectl -n apps scale deploy/homepage --replicas=0
ssh jcw@jcwpi "echo 1234 | sudo tee /var/lib/homepage/count.txt"
kubectl -n apps scale deploy/homepage --replicas=1
```
