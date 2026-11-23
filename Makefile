CGO_CFLAGS_ALLOW_PATTERN = -fno-short-enums

fmt-check:
	test -z "$(shell gofmt -l .)"

unit-test:
	CGO_CFLAGS_ALLOW='$(CGO_CFLAGS_ALLOW_PATTERN)' tinygo test -target=esp32c3-qemu.json ./...

UNUSED_HEADERS = esp32c2 esp32c5 esp32c61 esp32h2 esp32s2 \
	esp_bt_cfg.h libunwind.h nvs_handle.hpp nvs_bootloader.h esp_dpp.h \
	esp_wps.h smartconfig_ack.h esp_xt_wdt.h

update: update-esp-wifi
	find blobs/include -mindepth 1 -maxdepth 1 ! -name local -exec rm -rf {} +
	rm -rf blobs/libs
	mkdir -p blobs/libs
	cp -rp esp-wifi/c/include/. blobs/include
	cd blobs/include && rm -rf $(UNUSED_HEADERS)
	cp -rp esp-wifi/esp-wifi-sys-esp32/libs  blobs/libs/esp32
	cp -rp esp-wifi/esp-wifi-sys-esp32c3/libs blobs/libs/esp32c3
	cp -rp esp-wifi/esp-wifi-sys-esp32s3/libs blobs/libs/esp32s3
	cp -rp esp-wifi/esp-wifi-sys-esp32c6/libs blobs/libs/esp32c6
	$(MAKE) patch-esp32 patch-esp32s3

patch-esp32s3:
	go run ./tools/patch_xtensa_literals.go blobs/libs/esp32s3/*.a

patch-esp32:
	go run ./tools/patch_xtensa_literals.go blobs/libs/esp32/*.a

SMOKE_TARGETS ?= xiao-esp32c3 xiao-esp32s3 esp32-mini32 xiao-esp32c6
SMOKE_JOBS ?= 1

smoke-test:
	mkdir -p build
	rm -rf build/*
	@for target in $(SMOKE_TARGETS); do \
		mkdir -p build/$$target; \
		for example in ./examples/*/; do \
			echo $$target $$example; \
		done; \
	done | xargs -n 2 -P $(SMOKE_JOBS) sh -c '\
		cmd="tinygo build -target=$$0 -size short -o build/$$0/$$(basename $$1) $$1"; \
		out=$$(CGO_CFLAGS_ALLOW="$(CGO_CFLAGS_ALLOW_PATTERN)" $$cmd 2>&1); status=$$?; \
		printf "%s\n%s\n" "$$cmd" "$$out"; \
		exit $$status'

update-esp-wifi:
	git submodule update --init esp-wifi
	git -C esp-wifi pull --rebase origin main
