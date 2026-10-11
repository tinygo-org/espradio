CGO_CFLAGS_ALLOW_PATTERN = -fno-short-enums

fmt-check:
	test -z "$(shell gofmt -l .)"

unit-test:
	CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo test -target=esp32c3-qemu.json ./...

UNUSED_HEADERS = esp32c2 esp32c5 esp32c6 esp32c61 esp32h2 esp32s2 nimble \
	esp_bt_cfg.h libunwind.h nvs_handle.hpp nvs_bootloader.h esp_dpp.h \
	esp_wps.h smartconfig_ack.h esp_xt_wdt.h

update: update-esp-wifi
	rm -rf blobs/local.keep
	mv blobs/include/local blobs/local.keep
	rm -rf blobs/include
	rm -rf blobs/libs
	mkdir -p blobs/libs
	cp -rp esp-wifi/c/include      blobs
	cd blobs/include && rm -rf $(UNUSED_HEADERS)
	mv blobs/local.keep blobs/include/local
	cp -rp esp-wifi/esp-wifi-sys-esp32/libs  blobs/libs/esp32
	cp -rp esp-wifi/esp-wifi-sys-esp32c3/libs blobs/libs/esp32c3
	cp -rp esp-wifi/esp-wifi-sys-esp32s3/libs blobs/libs/esp32s3
	$(MAKE) patch-esp32 patch-esp32s3

patch-esp32s3:
	go run ./tools/patch_xtensa_literals.go blobs/libs/esp32s3/*.a

patch-esp32:
	go run ./tools/patch_xtensa_literals.go blobs/libs/esp32/*.a

smoke-test:
	mkdir -p build
	rm -rf build/*
	@for example in ./examples/*/; do \
		for target in xiao-esp32c3 xiao-esp32s3 esp32-mini32; do \
			echo "CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo build -target=$$target -size short -o build/$$(basename $$example) $$example"; \
			CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo build -target=$$target -size short -o build/$$(basename $$example) $$example || exit 1; \
		done; \
	done

update-esp-wifi:
	git submodule update --init esp-wifi
	git -C esp-wifi pull --rebase origin main
