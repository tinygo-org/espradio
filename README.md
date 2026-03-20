# espradio

TinyGo package for using the radio onboard ESP32 for wireless communication. Work in progress.

## Examples

### ap

Acts as a WiFi access point.

```
$ tinygo flash -target xiao-esp32c3 -monitor -stack-size 8kb ./examples/ap/
Connected to ESP32-C3
Flashing: 581408/581408 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 770fccb41a90d447b33527ef3baae2c719d9c9cbe8ad73acbc3d456d5ea586e7
Expected: c749be1b76496c30b98bc01f24c69ac126289014b1520653a5056956dcf69164
Attempting to boot anyway...
entry 0x40394cb0
ap: enabling radio...
ap: starting AP...
ap: starting L2 netdev (AP)...
ap: creating lneto stack...
ap: configuring DHCP server...
ap: AP is running on 192.168.4.1 — connect to espradio-ap
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
ap: rx_cb= 0 rx_drop= 0
```

### connect-and-dhcp

Connects to a WiFi access point, then makes an HTTP request.

```
$ tinygo flash -target xiao-esp32c3 -monitor -stack-size 8kb ./examples/connect-and-dhcp/
Connected to ESP32-C3
Flashing: 577968/577968 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: b3d321c6da3560568874b48a5b5943d6e026512435c867dcdbb421ff139e2994
Expected: b2b8b1f7233816d2371dbcd6722c0ce1859fc92151a194458fb54b2cf48f7ceb
Attempting to boot anyway...
entry 0x40394b10
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
alive
```

### scan

Scans for WiFi access points.

```
$ tinygo flash -target xiao-esp32c3 -monitor ./examples/scan/
Connected to ESP32-C3
Flashing: 382416/382416 bytes (100%)
Connected to /dev/ttyACM0. Press Ctrl-C to exit.
SHA-256 comparison failed:
Calculated: 9c3137e3d93cb56d55e2b6293b893476a63ee5e922c330204e51bb85ea1c7e76
Expected: f0e316663f9508752ad6114637bfa6cd63f895f5425c09a682843019e07005b7
Attempting to boot anyway...
entry 0x4038d948
initializing radio...
starting radio...
scanning WiFi...
AP: rems RSSI -47
AP: rems RSSI -81
scan finished
```

### starting

Starts the ESP32 radio.

