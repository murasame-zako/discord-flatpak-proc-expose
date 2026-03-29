target="linux/386
linux/amd64
linux/arm
linux/arm64
linux/loong64
linux/mips
linux/mips64
linux/mips64le
linux/mipsle
linux/ppc64
linux/ppc64le
linux/riscv64
linux/s390x
"
filename=proc-expose
for t in $target
do
echo trying build for $t target.
export GOOS=$(echo $t | cut -d '/' -f 1)
export GOARCH=$(echo $t | cut -d '/' -f 2)
export CGO_ENABLED=0
go build -o $filename-$GOARCH $filename.go
done