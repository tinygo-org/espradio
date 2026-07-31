
## Examples

### mqtt

Uses the MQTT machine to machine protocol to publish and subscribe to messages with the `broker.hivemq.com` test server. Uses the Go stdlib and the [`natiu-mqtt`](github.com/soypat/natiu-mqtt) package with the `netlink` interface.

```
$ tinygo flash -target xiao-esp32s3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/mqtt/
   code    data     bss |   flash     ram
 672497   22956  284464 |  695453  307420
...
Connecting to WiFi...
Connected to WiFi.
ClientId: tinygo-client-WYVKRWBJRP
Connecting to MQTT broker at broker.hivemq.com:1883
TCP connected to 3.122.68.120:1883
Sending MQTT CONNECT...
MQTT CONNECT succeeded
Subscribed to topic cpu/usage
Message Random value: 34 received on topic cpu/usage
Message Random value: 8 received on topic cpu/usage
Message Random value: 32 received on topic cpu/usage
...
```

### apwebserver

Brings the device up as a WiFi access point with its own DHCP server and serves
the same page as [webserver](#webserver). Clients get an address automatically;
browse to `http://192.168.4.1` once connected.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -size short -monitor ./examples/apwebserver
   code    data     bss |   flash     ram
 695624   22700   90436 |  718324  113136
...
Starting AP...
HTTP server listening on http://192.168.4.1:80
```

### http-hello

Minimal `httphi` webserver answering `{"message":"hello"}` on `/`. This is the
[getting started](#getting-started-with-http-hello-example) example.

```
tinygo flash -target xiao-esp32s3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/http-hello
```

### webserver

![webserver image](./images/webserver.png)

Serves an embedded page with `httphi`, driving the on-board LED and a counter
over `fetch()` from the browser. Shows routing several paths on a
`httphi.MuxSlice`, and reading a POSTed form with `httpraw.Form`:

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/webserver/
   code    data     bss |   flash     ram
 695130   22692   90460 |  717822  113152
...
Connecting to WiFi...
HTTP server listening on http://192.168.1.46:80
```

## Examples - `espradio` and `lneto` directly

These skip `netdev`/`netlink` and drive the radio, and where they need one the
`lneto` stack, themselves.

### ap

Shows how to set up a WiFi access point with a DHCP server using the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=tinygoap -X main.password=YourPasswordHere" -monitor ./examples/ap
   code    data     bss |   flash     ram
 582004   21988  257010 |  603992  278998
...
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
...
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

Connects to a WiFi access point, calls NTP to obtain the current date/time, then serves a tiny web application with `httphi` on the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor ./examples/http-app/
...
got IP: 192.168.1.46
resolving ntp host: pool.ntp.org
NTP success: 2026-03-21 08:51:31.136908291 +0000 UTC m=+3.079340401
listening on http://192.168.1.46:80
...
```

### http-static

Minimal HTTP server using `httphi` that serves a static embedded webpage on the low-level lneto interface.

```
$ tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=YourPasswordHere" -monitor ./examples/http-static/
...
listening on http://192.168.1.46:80
incoming connection: 192.168.1.223 from port 53636
incoming connection: 192.168.1.223 from port 53640
Got webpage request!
```

### scan

Scans for WiFi access points.

```
$ tinygo flash -target xiao-esp32c3 -monitor ./examples/scan
...
scanning WiFi...
AP: rems RSSI -59
AP: rems RSSI -78

scanning WiFi...
AP: rems RSSI -59
AP: rems RSSI -79
```

### espnow

Sends and receives ESP-NOW packets, the connectionless Espressif protocol that
talks peer to peer without an access point or any TCP/IP stack. Set
`peerAddress` to the other device's ESP-NOW address, which each device prints on
boot, then flash both.

```
tinygo flash -target xiao-esp32c3 -monitor ./examples/espnow
```

### http-stdlib
Use the standard library net/http package for hosting a HTTP server.
```
tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/http-stdlib
```

### http-get

Fetches a URL with `http.Get()`.

```
tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=yourssid -X main.password=yourpassword" -size short -monitor ./examples/http-get
```