# espradio

TinyGo package for using the radio onboard ESP32 for wireless communication. Work in progress.

## Examples

### ap

Shows how to set up a WiFi access point with a DHCP server.

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

Connects to a Wi-Fi network and gets an IP address with DHCP.

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

Connects to a WiFi access point, calls NTP to obtain the current date/time, then serves a tiny web application.

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

Minimal HTTP server that serves a static webpage.

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

