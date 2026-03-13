#!/bin/sh
systemctl daemon-reload
systemctl enable lplex-server || true
