package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"

	mqtt "github.com/soypat/natiu-mqtt"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
	broker   string = "broker.hivemq.com:1883"
	topic    string = "cpu/usage"
)

func main() {
	time.Sleep(3 * time.Second)

	if err := connectToWifi(); err != nil {
		failure("WiFi connect failed: " + err.Error())
	}

	clientId := "tinygo-client-" + randomString(10)
	fmt.Printf("ClientId: %s\n", clientId)

	// Get a transport for MQTT packets.
	// Retry TCP connection since public brokers may reject/close connections under load.
	fmt.Printf("Connecting to MQTT broker at %s\n", broker)
	var conn net.Conn
	for attempt := range 5 {
		var err error
		conn, err = net.Dial("tcp", broker)
		if err != nil {
			fmt.Printf("net.Dial attempt %d failed: %s\n", attempt+1, err)
			time.Sleep(2 * time.Second)
			continue
		}
		break
	}
	if conn == nil {
		failure("all TCP connection attempts failed")
	}
	fmt.Printf("TCP connected to %v\n", conn.RemoteAddr())
	defer conn.Close()

	// Create new client
	client := mqtt.NewClient(mqtt.ClientConfig{
		Decoder: mqtt.DecoderNoAlloc{make([]byte, 1500)},
		OnPub: func(_ mqtt.Header, _ mqtt.VariablesPublish, r io.Reader) error {
			message, _ := io.ReadAll(r)
			fmt.Printf("Message %s received on topic %s\n", string(message), topic)
			return nil
		},
	})

	// Connect client
	var varconn mqtt.VariablesConnect
	varconn.SetDefaultMQTT([]byte(clientId))
	varconn.KeepAlive = 60 // seconds; some brokers reject KeepAlive=0
	fmt.Println("Sending MQTT CONNECT...")
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	err := client.Connect(ctx, conn, &varconn)
	if err != nil {
		failure("failed to connect: " + err.Error())
	}
	fmt.Println("MQTT CONNECT succeeded")

	// Subscribe to topic
	ctx, _ = context.WithTimeout(context.Background(), 10*time.Second)
	err = client.Subscribe(ctx, mqtt.VariablesSubscribe{
		PacketIdentifier: 23,
		TopicFilters: []mqtt.SubscribeRequest{
			{TopicFilter: []byte(topic), QoS: mqtt.QoS0},
		},
	})
	if err != nil {
		failure("failed to subscribe to " + topic + ": " + err.Error())
	}
	fmt.Printf("Subscribed to topic %s\n", topic)

	// Publish on topic
	pubFlags, _ := mqtt.NewPublishFlags(mqtt.QoS0, false, false)
	pubVar := mqtt.VariablesPublish{
		TopicName: []byte(topic),
	}

	for i := 0; i < 10; i++ {
		if !client.IsConnected() {
			failure("client disconnected: " + client.Err().Error())
		}

		payload := fmt.Sprintf("Random value: %d", randomInt(0, 100))

		pubVar.PacketIdentifier++
		err = client.PublishPayload(pubFlags, pubVar, []byte(payload))
		if err != nil {
			failure("error transmitting message: " + err.Error())
		}

		time.Sleep(time.Second)

		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		err = client.HandleNext()
		if err != nil {
			failure("handle next: " + err.Error())
		}

	}

	client.Disconnect(errors.New("disconnected gracefully"))
	println("Disconnected from MQTT broker.")

	for {
		select {}
	}
}

// Returns an int >= min, < max
func randomInt(min, max int) int {
	return min + rand.Intn(max-min)
}

// Generate a random string of A-Z chars with len = l
func randomString(len int) string {
	bytes := make([]byte, len)
	for i := 0; i < len; i++ {
		bytes[i] = byte(randomInt(65, 90))
	}
	return string(bytes)
}

func connectToWifi() error {
	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("Connecting to WiFi...")
	for range 3 {
		err := link.NetConnect(&nl.ConnectParams{
			Ssid:       ssid,
			Passphrase: password,
		})
		if err == nil {
			println("Connected to WiFi.")
			return nil
		}
		println("Failed to connect to WiFi", err.Error())
		time.Sleep(5 * time.Second)
	}

	return errors.New("failed to connect to WiFi after 3 attempts")
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
