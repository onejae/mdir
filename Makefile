.PHONY: build test check clean

build:
	go build -buildvcs=false -o bin/mdir-fm ./cmd/mdir-fm

test:
	go test -race ./...

check:
	go vet ./...

clean:
	rm -f bin/mdir-fm bin/mdir
