TARGET := exteditor
SRCS := main.go

.PHONY: all clean
.PHONY: build rebuild logbuild dbgbuild
.PHONY: pkg linux macos windows

all: build

clean:
	rm -rf $(TARGET) output

rebuild: clean
	$(MAKE) all

build:
	go build -o $(TARGET)

logbuild:
	go build -tags=enable_log -o $(TARGET)

dbgbuild:
	go build -race -o $(TARGET)

pkg: linux macos windows

linux: $(SRCS)
	GOOS=linux GOARCH=amd64 go build -o output/linux/$(TARGET)

macos: $(SRCS)
	GOOS=darwin GOARCH=arm64 go build -o output/macos/$(TARGET)

windows: $(SRCS)
	GOOS=windows GOARCH=amd64 go build -o output/windows/$(TARGET).exe

