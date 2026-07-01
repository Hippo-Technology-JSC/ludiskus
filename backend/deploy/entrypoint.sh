#!/bin/sh
# Chọn binary theo LUDISKUS_ROLE (docs/12).
if [ "$LUDISKUS_ROLE" = "worker" ]; then
    exec ludiskus-worker
else
    exec ludiskus
fi
