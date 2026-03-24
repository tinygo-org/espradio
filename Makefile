update: update-esp-wifi
	rm -rf blobs/headers
	rm -rf blobs/include
	rm -rf blobs/libs
	mkdir -p blobs/libs
	cp -rp esp-wifi/c/headers      blobs
	cp -rp esp-wifi/c/include      blobs
	cp -rp esp-wifi/esp-wifi-sys-esp32c3/libs blobs/libs/esp32c3

update-esp-wifi:
	cd esp-wifi && git pull --rebase origin main
