# eriphe-printer

Prints on the printers of the local network over IPP, with no CUPS and no driver. It sends a PDF as
it is when the printer reads PDF. Otherwise it draws the pages itself in the raster the printer
reads: Apple raster (`image/urf`, every AirPrint printer) or PWG raster (`image/pwg-raster`, IPP
Everywhere). Most home printers read only a raster. A PDF's pages are drawn by poppler's
`pdftoppm`, which the image carries.

It follows the contract of [servers/README.md](../README.md): eriphe-core launches it on a device
and writes its configuration.

## Configuration

Its accounts are its printers: a name and an address. There is no secret.

```json
{
  "accounts": [{"name": "bureau", "url": "ipp://192.0.2.10/ipp/print"}],
  "timeout_seconds": 120
}
```

| Key | What |
|---|---|
| `accounts[].name` | how the tools name the printer (`account`) |
| `accounts[].url` | `ipp://<address>[:port]/<path>` or `ipps://…`. The port defaults to 631 and the path to `/ipp/print`, which most printers use. Over `ipps`, the printer's own certificate is not checked: home printers make their own. |
| `timeout_seconds` | one exchange with a printer, 120 by default: a long document takes time to send |

The address is typed by the user. Nothing discovers printers: mDNS does not cross the Docker
network a service runs on, and probing a network the container does not know is unreliable. Give
the printer a fixed address on the router.

The service needs egress, so it can reach the printer on the LAN. It writes only in `/tmp`, where
it draws the pages.

## Tools

| Tool | Arguments | Answer | Annotations |
|---|---|---|---|
| `printer_status` | `account` (may be left out with one printer) | `{printer, model, state, reasons, message, color, two_sided, paper, markers: [{name, level}]}`: `state` is `idle`, `printing` or `stopped`; `reasons` are the printer's (`media-empty`, `toner-low`…), none when all is well; `level` is a percentage, -1 when unknown | read-only |
| `print` | `account`, `document`, `title`, `copies` (1 to 50), `sides` (`one-sided`, `two-sided-long-edge`, `two-sided-short-edge`), `color` (`false`: black and white on a color printer) | `{printer, job_id, state, pages, format}`: the job the printer took, what was sent, and the pages drawn when it was a raster | open world, not destructive: Send in the access grid |

`document` is one of a node's files. `_meta: {"eriphe/files": ["document"]}` declares it: the brain
names the file (`media:<node id>/<name>`), and eriphe-core sends its bytes as a `data:` URI. The
server takes a PDF, a JPEG or a PNG, recognised by its bytes, 8 MiB at most (a node's file is 5 MiB at most); over HTTP a call's body may weigh 12 MiB. It refuses anything
else, or a `document` that is not a `data:` URI.

`print` refuses before sending anything when the printer is stopped (with its own message, such as
« plus de papier »), when it does not print the sides asked, or when it reads none of the formats
this server sends. `print` also carries `eriphe/says`, the sentence of its card
(« Imprimer « Recette » en 2 exemplaires »).

**Raster.** The pages are drawn in 8-bit gray, or 8-bit sRGB when the printer prints in color and
color is asked. Each page is fitted to the printer's default paper (`media-default`, A4 when it
names none) at 600 dpi when the printer offers it. The page is turned a quarter when its
orientation differs from the paper's, and centred. The back of a sheet is flipped as the printer
says (`pwg-raster-document-sheet-back`, or `DM` in `urf-supported`). When the printer does not
make copies of a raster itself, the pages are repeated. Each page is compressed as soon as it is
drawn, so one page at a time is held whole. A document has 200 pages at most.

### Setup tool

`test_account` (`_meta: {"eriphe/setup": true}`) asks the printer for its attributes: `{ok, error,
detail}`, where `detail` is its model (« Brother DCP-L2530DW series »), shown by the workspace
after the printer is added.

## Build and test

```sh
go build ./... && go vet ./... && go test -race ./...
docker build -t eriphe-printer:dev .
```

The tests use a fake IPP printer (`fake_test.go`). They decode the rasters the server writes back
to pixels. `TestPdftoppm` draws a real PDF when `pdftoppm` is installed (CI installs it), and is
skipped otherwise.
