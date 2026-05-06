CGO_CFLAGS_ALLOW_PATTERN = -fno-short-enums

fmt-check:
	test -z "$(shell gofmt -l .)"

unit-test:
	CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo test -target=esp32c3-qemu.json ./...

update: update-esp-wifi
	rm -rf blobs/headers
	rm -rf blobs/include
	rm -rf blobs/libs
	mkdir -p blobs/libs
	cp -rp esp-wifi/c/headers      blobs
	cp -rp esp-wifi/c/include      blobs
	cp -rp esp-wifi/esp-wifi-sys-esp32c3/libs blobs/libs/esp32c3
	cp -rp esp-wifi/esp-wifi-sys-esp32s3/libs blobs/libs/esp32s3

patch-esp32s3:
	go run ./tools/patch_xtensa_literals.go blobs/libs/esp32s3/*.a

smoke-test:
	mkdir -p build
	rm -rf build/*
	@for example in ./examples/*/; do \
		for target in xiao-esp32c3 xiao-esp32s3; do \
			echo "CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo build -target=$$target -size short -o build/$$(basename $$example) $$example"; \
			CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo build -target=$$target -size short -o build/$$(basename $$example) $$example || exit 1; \
		done; \
	done

update-esp-wifi:
	cd esp-wifi && git pull --rebase origin main
