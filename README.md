# espradio

TinyGo package for using the radio onboard ESP32 for wireless communication. Work in progress.

Already functions on the `esp32c3` and `esp32s3` processors for WiFi communication. More processors coming soon!

Bluetooth is still in progress.

## Examples

### ap

Shows how to set up a WiFi access point with a DHCP server using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=tinygoap -X main.password=YourPasswordHere" -monitor -stack-size 8kb ./examples/ap
Connected to ESP32-C3
Flashing: 581584/581584 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: dfa22060430ed9033cf25f30ff9d91d940f32fbbf2fffe1abbaec4cd0f8e7389
Expected: 6d9b75f1612e777d952efe3be8dc8cf33d2b9db4314d8cbfddf6d25a01b9c9cb
Attempting to boot anyway...
entry 0x40394dc8
ap: enabling radio...
ap: starting AP...
ap: starting L2 netdev (AP)...
ap: creating lneto stack...
ap: configuring DHCP server...
ap: AP is running on 192.168.4.1 - connect to tinygoap
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
...
```

### connect-and-dhcp

Connects to a Wi-Fi network and gets an IP address with DHCP using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor -stack-size 8kb ./examples/connect-and-dhcp
Connected to ESP32-C3
Flashing: 578032/578032 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: ac0c75a476a9dc3469142d5191d87eb30b499eb61356567eebc1f524e2d4188b
Expected: 330af5651f0cca07a494b68ab84b5631c68a10f9bd2e5568f7145eb7efcc5a9d
Attempting to boot anyway...
entry 0x40394c20
initializing radio...
starting radio...
connecting to rems ...
connected to rems !
starting L2 netdev...
creating lneto stack...
starting DHCP...
got IP: 192.168.1.46
gateway: 192.168.1.1
DNS: 192.168.1.1
done!
alive
alive
...
```

### http-app

Connects to a WiFi access point, calls NTP to obtain the current date/time, then serves a tiny web application using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor -stack-size 8kb ./examples/http-app/
Connected to ESP32-C3
Flashing: 652848/652848 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 5520848628102c249831cc101bbd042d6311260e697288fb5bf082ad4c912b32
Expected: 8b36e3d705b1ed66d74082f300d35a796d6153344e7e8b39eccdbaa2f0bff23f
Attempting to boot anyway...
entry 0x40395708
initializing radio...
starting radio...
connecting to rems ...
connected to rems !
starting L2 netdev...
creating lneto stack...
starting DHCP...
got IP: 192.168.1.46
resolving ntp host: pool.ntp.org
NTP success: 2026-03-21 08:51:31.136908291 +0000 UTC m=+3.079340401
listening on http://192.168.1.46:80
...
```

### http-static

Minimal HTTP server that serves a static webpage using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor -stack-size 8kb ./examples/http-static/
Connected to ESP32-C3
Flashing: 627504/627504 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 468629c0cff3cf13345660532bcc748a1a93df46c197d51727d75863bd985195
Expected: 785df5a5bb20c057bcc0580b2870982aa779faeb1f946a5d3c371ad36da077f0
Attempting to boot anyway...
entry 0x40395350
initializing radio...
starting radio...
connecting to rems ...
connected to rems !
starting L2 netdev...
creating lneto stack...
listening on http://192.168.1.46:80
incoming connection: 192.168.1.223 from port 53636
incoming connection: 192.168.1.223 from port 53640
Got webpage request!
```

### mqtt

Uses the MQTT machine to machine protocol to publish and subscribe to messages with the test.mosquitto.org server. Uses the Go stdlib and the [`natiu-mqtt`](github.com/soypat/natiu-mqtt) package with the `netlink` interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/mqtt
code    data     bss |   flash     ram
698122   22700  273178 |  720822  295878
Connected to ESP32-C3
Flashing: 720928/720928 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
load:0x4202c6f0,len:0x83900
SHA-256 comparison failed:
Calculated: b7e1d566962176bdc79440051ce901ce1e70a5a236d48f82e83684edef7ccfeb
Expected: 74ff2b47841741b35f58f4091f4da93d7e613fbde43fbc082967d06752da3804
Attempting to boot anyway...
entry 0x4039828c
Connecting to WiFi...
Connected to WiFi.
ClientId: tinygo-client-TJMEVOCXRJ
Connecting to MQTT broker at test.mosquitto.org:1883
Subscribed to topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Message 160.00Mhz received on topic cpu/freq
Disconnected from MQTT broker.
```

### scan

Scans for WiFi access points.

```
$ tinygo flash -target xiao-esp32c3 -monitor -stack-size 8kb ./examples/scan
Connected to ESP32-C3
Flashing: 442736/442736 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 0045ab8467d485eb94005908a8e7f9dd7baf4dfa610f20ed67884c8ae5e98737
Expected: 6be1722a792dec8849a2a9fb26c8faf50ba48294ea6617e125472469e56ea719
Attempting to boot anyway...
entry 0x4038e3d0
initializing radio...
starting radio...
scanning WiFi...
AP: rems RSSI -59
AP: rems RSSI -78

scanning WiFi...
AP: rems RSSI -59
AP: rems RSSI -79
```

### starting

Starts the ESP32 radio.

### webserver

Runs a webserver using the Go `net/http` package using the `netlink` interface:

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -stack-size 8kb -monitor ./examples/webserver/
Connected to ESP32-C3
Flashing: 953984/953984 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
load:0x403918e4,len:0xa474
load:0x42037c0c,len:0xb1248
SHA-256 comparison failed:
Calculated: 87698da75b5f09de7723e8650f1c84416180cfad80a2800e1adeb04c6d6f2087
Expected:
2ca7343abec2d068cbb8f39247e44d8aca94e5f0b78f623c7b7eb8981d8499cc
Attempting to boot anyway...
entry 0x4039bd14
Connecting to WiFi...
HTTP server listening on http://192.168.1.46:80
```

## Updating `esp-wifi-sys`

This package uses files from the [`esp-wifi-sys`](https://github.com/esp-rs/esp-wifi-sys) package, then copies the needed ones into the `blobs` directory.

To update these dependencies to the latest version, run the `make update` command. This will update the submodule, then copy the needed files. Then run `make patch-esp32s3` to patch the blobs for the LLD linker. Note that this may break existing functionality requiring changes to TinyGo linker files or other changes.
