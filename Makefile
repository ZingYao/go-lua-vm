.PHONY: fmt test vet gate compat bench build-glua release-glua

fmt:
	gofmt -w $$(find . -path ./third_party -prune -o -name '*.go' -print)

test:
	CGO_ENABLED=0 go test ./...

vet:
	CGO_ENABLED=0 go vet ./...

gate:
	./scripts/check-go-gates.sh

compat:
	./scripts/compare-cli-golden.sh

bench:
	CGO_ENABLED=0 go test -bench=. ./...

build-glua:
	./scripts/build-glua.sh

release-glua:
	./scripts/release-glua.sh
