BINARY := bin/mcpgen
PKG    := ./cmd/mcpgen

.PHONY: build test install tidy clean validate-eazyupdates tools-eazyupdates

build:
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

tidy:
	go mod tidy

install: build
	install -m 0755 $(BINARY) $$HOME/.local/bin/mcpgen

clean:
	rm -rf bin

validate-eazyupdates: build
	$(BINARY) validate --config=examples/eazyupdates/config.yaml

tools-eazyupdates: build
	$(BINARY) tools --config=examples/eazyupdates/config.yaml
