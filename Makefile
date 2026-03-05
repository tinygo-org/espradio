update: esp-wifi/README.md
	rm -rf blobs/headers
	rm -rf blobs/include
	rm -rf blobs/libs
	mkdir -p blobs/libs/esp32c3
	cp -rp esp-wifi/c/headers                          blobs
	cp -rp esp-wifi/c/include                          blobs
	cp -rp esp-wifi/esp-wifi-sys-esp32c3/libs/*.a      blobs/libs/esp32c3

esp-wifi/README.md:
	git clone https://github.com/esp-rs/esp-wifi

.PHONY: build-blobs build-blogs
build-blobs: esp-wifi/README.md
	./build/build-idf-blobs-docker.sh

build-blogs: build-blobs
