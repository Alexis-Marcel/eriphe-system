#!/bin/sh
# Prototype only (proto/eriphe-coche): every screenshot of PROTO.md, again.
# From core/web/espace, with the dev server on 5317 (npx vite --port 5317).
set -e
cd "$(dirname "$0")/.."
for t in light dark; do
  for s in desktop phone; do
    node proto-shots/coche.mjs "$t" "$s"
  done
done
