.PHONY: build install clean

build:
	go build -o tgcp ./cmd/tgcp

install:
	go install ./cmd/tgcp

clean:
	rm -f tgcp
