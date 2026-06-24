.PHONY: all clean native-build

all: native-build

clean:
	$(MAKE) -C native clean

native-build:
	$(MAKE) -C native


