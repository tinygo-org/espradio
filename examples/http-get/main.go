// This example gets an URL using http.Get().  URL scheme can be http or https.
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
)

func main() {
	// wait a bit for serial
	time.Sleep(2 * time.Second)

	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("Connecting to WiFi...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:            ssid,
		Passphrase:      password,
		ConnectTimeout:  time.Minute,
		Retries:         5,
		ConnectMode:     nl.ConnectModeSTA,
		AuthType:        nl.AuthTypeWPA2Mixed,
		WatchdogTimeout: time.Minute,
	})
	if err != nil {
		failure("WiFi connect failed: " + err.Error())
	}

	println("Connected. Getting URL...")
	name := "John Doe"
	occupation := "gardener"

	params := "name=" + url.QueryEscape(name) + "&" +
		"occupation=" + url.QueryEscape(occupation)

	path := fmt.Sprintf("http://httpbin.org/get?%s", params)

	cnt := 0
	for {
		fmt.Printf("Getting %s\r\n\r\n", path)
		resp, err := http.Get(path)
		if err != nil {
			fmt.Printf("problem is %s\r\n", err.Error())
			time.Sleep(10 * time.Second)
			continue
		}

		fmt.Printf("%s %s\r\n", resp.Proto, resp.Status)
		for k, v := range resp.Header {
			fmt.Printf("%s: %s\r\n", k, strings.Join(v, " "))
		}
		fmt.Printf("\r\n")

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("%s\r\n", err.Error())
			time.Sleep(10 * time.Second)
			continue
		}
		println(string(body))
		resp.Body.Close()

		cnt++
		fmt.Printf("-------- %d --------\r\n", cnt)
		time.Sleep(10 * time.Second)
	}
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
