#!/bin/sh
# Prototype only (#730): every screenshot of PROTO.md, again.
# From core/web/espace, with the dev server on 5317 (npx vite --port 5317).
set -e
U=http://localhost:5317
cd "$(dirname "$0")/.."
for t in light dark; do
  node proto-shots/shoot.mjs "$U/?blocks" "proto-shots/blocks-desktop-$t.png" $t desktop
  H=2600 node proto-shots/shoot.mjs "$U/?open=node-day" "proto-shots/day-desktop-$t.png" $t desktop article.pg
  H=2600 node proto-shots/shoot.mjs "$U/?open=node-mails" "proto-shots/mails-desktop-$t.png" $t desktop article.pg
  H=2600 node proto-shots/shoot.mjs "$U/?open=node-invest" "proto-shots/invest-desktop-$t.png" $t desktop article.pg
  H=4200 node proto-shots/shoot.mjs "$U/?open=node-day" "proto-shots/day-phone-$t.png" $t phone phone-page
  H=4200 node proto-shots/shoot.mjs "$U/?open=node-mails" "proto-shots/mails-phone-$t.png" $t phone phone-page
  H=4200 node proto-shots/shoot.mjs "$U/?open=node-invest" "proto-shots/invest-phone-$t.png" $t phone phone-page
done
node proto-shots/shoot.mjs "$U/?blocks" "proto-shots/blocks-phone-light.png" light phone
