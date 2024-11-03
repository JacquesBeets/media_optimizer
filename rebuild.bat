@echo off
git pull
go build -o media_optimizer.exe
taskkill /F /IM media_optimizer.exe
start media_optimizer.exe
