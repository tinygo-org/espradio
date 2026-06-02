// This example demonstrates ESP-NOW packet and stream-style communication using
// the managed espradio ESPNow API.
//
// Set peerAddress to the ESP-NOW MAC address of the remote device before
// flashing. For two devices running this example, each device should use the
// other device's printed local ESP-NOW address as peerAddress.
//
// tinygo flash -target xiao-esp32c3 -monitor ./examples/espnow
package main

import (
	"errors"
	"os"
	"time"

	"tinygo.org/x/espradio"
)

var peerAddress = espradio.ESPNowAddr{0x24, 0x6f, 0x28, 0xaa, 0xbb, 0xcc}

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	if err := espradio.Enable(espradio.Config{}); err != nil {
		failure("could not enable radio: " + err.Error())
	}

	println("starting radio...")
	if err := espradio.Start(); err != nil {
		failure("could not start radio: " + err.Error())
	}

	now, err := espradio.NewESPNow(espradio.ESPNowConfig{})
	if err != nil {
		failure("could not initialize ESP-NOW: " + err.Error())
	}
	defer now.Close()

	peer, err := now.AddPeer(espradio.PeerConfig{
		Address: peerAddress,
		If:      espradio.WiFiInterfaceSTA,
	})
	if err != nil {
		failure("could not add peer: " + err.Error())
	}
	defer peer.Close()

	println("local ESP-NOW address:", peer.LocalESPNowAddr().String())
	println("peer ESP-NOW address:", peer.Addr().String())

	if _, err := peer.WriteTo([]byte("hello"), nil); err != nil {
		failure("could not send packet: " + err.Error())
	}
	println("sent packet")

	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		failure("could not set read deadline: " + err.Error())
	}
	buf := make([]byte, espradio.ESPNowMaxDataLength)
	n, addr, err := peer.ReadFrom(buf)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			println("no reply received before deadline")
		} else {
			failure("could not read packet: " + err.Error())
		}
	} else {
		println("received", n, "bytes from", addr.String())
		println("payload:", string(buf[:n]))
	}

	stream := peer.Stream()
	if _, err := stream.Write([]byte("streamed payload")); err != nil {
		failure("could not send stream payload: " + err.Error())
	}
	println("sent stream payload")

	broadcast, err := now.Broadcast()
	if err != nil {
		failure("could not add broadcast peer: " + err.Error())
	}
	if _, err := broadcast.WriteTo([]byte("announcement"), nil); err != nil {
		failure("could not send broadcast: " + err.Error())
	}
	println("sent broadcast announcement")
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(time.Second)
	}
}
