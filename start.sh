#!/usr/bin/env bash
# Ein Befehl - SpeedNAS laeuft.  Alle Befehle: ./start.sh --help
exec "$(dirname "$0")/scripts/speednas.sh" "$@"
