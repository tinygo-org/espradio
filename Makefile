fmt-check:
	test -z "$(shell gofmt -l .)"

unit-test:
	tinygo test -target=esp32c3-qemu.json ./...

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

update-esp-wifi:
	cd esp-wifi && git pull --rebase origin main
