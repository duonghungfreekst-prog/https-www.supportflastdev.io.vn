Stop-Process -Name "supportflast" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "supportflast_engine" -Force -ErrorAction SilentlyContinue

$env:GOGC = "200"
$env:GOMEMLIMIT = "2048MiB"

cd f:\supportflast.dev\supportflast_engine
go build -o supportflast.exe .
