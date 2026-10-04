#!/usr/bin/env sh
# Downloads the OFL fonts embedded in the dashboard. Run once; commit the results.
set -eu
cd "$(dirname "$0")/../web/fonts"
base=https://raw.githubusercontent.com/google/fonts/main/ofl
curl -fsSLo VT323-Regular.ttf        "$base/vt323/VT323-Regular.ttf"
curl -fsSLo OFL-VT323.txt            "$base/vt323/OFL.txt"
curl -fsSLo IBMPlexMono-Regular.ttf  "$base/ibmplexmono/IBMPlexMono-Regular.ttf"
curl -fsSLo IBMPlexMono-SemiBold.ttf "$base/ibmplexmono/IBMPlexMono-SemiBold.ttf"
curl -fsSLo OFL-IBMPlexMono.txt      "$base/ibmplexmono/OFL.txt"
ls -la
