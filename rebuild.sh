#!/bin/bash
git pull
systemctl stop media-optimizer
go build -o media-optimizer
systemctl start media-optimizer
