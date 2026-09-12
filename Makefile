GO_TOOLCHAIN ?= go1.25.0

.PHONY: build build-windows

build:
	GOTOOLCHAIN=$(GO_TOOLCHAIN) wails build

build-windows:
	GOTOOLCHAIN=$(GO_TOOLCHAIN) wails build -platform windows/amd64
