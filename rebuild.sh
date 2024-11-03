#!/bin/bash
git pull
go build -o media_optimizer
pkill media_optimizer
./media_optimizer &
