# espradio

TinyGo package for using the ESP32 onboard radio for wireless communication.

Already works on the `esp32c3` and `esp32s3` processors for WiFi. More processors coming soon!

Bluetooth is still in progress.

## Examples

### ap

Shows how to set up a WiFi access point with a DHCP server using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=tinygoap -X main.password=YourPasswordHere" -monitor ./examples/ap
   code    data     bss |   flash     ram
 582004   21988  257010 |  603992  278998
Connected to ESP32-C3
Flashing: 604096/604096 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 8d1512da059d76b08bbfe99c14ab43d792466ee8fd85a365b338a6ad17aa1e2a
Expected: 06fd1290b7002c716ccf8a9df263fb33d5e22b6892e08038cb6a8e8f6b04be5f
Attempting to boot anyway...
entry 0x40398b3c
ap: enabling radio...
ap: starting AP...
ap: starting L2 netdev (AP)...
ap: creating lneto stack...
ap: configuring DHCP server...
ap: AP is running on 192.168.4.1 - connect to tinygo-ap
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
...
```

### connect-and-dhcp

Connects to a Wi-Fi network and gets an IP address with DHCP using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32s3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor ./examples/connect-and-dhcp
   code    data     bss |   flash     ram
 574233   22304  259184 |  596537  281488
Connected to ESP32-S3
Flashing: 596640/596640 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 84318f99e1f97b458c8a4afc3237908a2d5be760b42c66b39c03367a96e2ae32
Expected: c3a26bf70f12f0ffc686e708a86b10548920a08e166a0b14d9e2a8f80e662d2e
Attempting to boot anyway...
entry 0x4038688c
initializing radio...
starting radio...
connecting to rems ...
connected to rems !
starting L2 netdev...
creating lneto stack...
starting DHCP...
got IP: 192.168.1.241
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
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor ./examples/http-app/
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
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor 8kb ./examples/http-static/
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
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/mqtt/
   code    data     bss |   flash     ram
 701066   22700  279290 |  723766  301990

Connected to ESP32-C3
Flashing: 723872/723872 bytes (100%)               
Connected to /dev/ttyACM0. Press Ctrl-C to exit.                                                          
load:0x4202c860,len:0x84310
SHA-256 comparison failed:
Calculated: 6bc25eb465fa7599f460725ba7ca7550c86094cb2addfb1fe513499539e0bdd5                                                                                                                                        
Expected: c4e21f71423096b6072c04d6e2e0c1c8809ea7dc92c5394e6ebba6a0dea25a79                                                                                                                                          
Attempting to boot anyway...              
entry 0x40398dc4                          
Connecting to WiFi... 
Connected to WiFi.                   
ClientId: tinygo-client-BFEIHFDOCT   
Connecting to MQTT broker at broker.hivemq.com:1883
TCP connected to 35.157.137.172:1883
Sending MQTT CONNECT...                                                                                   
MQTT CONNECT succeeded                                                                                    
Subscribed to topic cpu/usage                                                                             
Message Random value: 45 received on topic cpu/usage                                                      
Message Random value: 54 received on topic cpu/usage
Message Random value: 62 received on topic cpu/usage
Message Random value: 41 received on topic cpu/usage
Message Random value: 68 received on topic cpu/usage
Message Random value: 36 received on topic cpu/usage
Message Random value: 22 received on topic cpu/usage
Message Random value: 73 received on topic cpu/usage
Message Random value: 27 received on topic cpu/usage
Message Random value: 86 received on topic cpu/usage                                                      
Disconnected from MQTT broker.
```

### scan

Scans for WiFi access points.

```
$ tinygo flash -target xiao-esp32c3 -monitor ./examples/scan
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
