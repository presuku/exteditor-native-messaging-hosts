.PHONY: all clean build pkg

all: build

clean:
	$(MAKE) -C native clean
	rm -rf output

build:
	$(MAKE) -C native

pkg:
	./scripts/mkpkg.sh
