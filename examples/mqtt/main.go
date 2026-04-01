package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"machine"
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
	broker   string = "test.mosquitto.org:1883"
	topic    string = "cpu/freq"
)

func main() {
	time.Sleep(3 * time.Second)

	if err := connectToWifi(); err != nil {
		log.Fatal(err)
	}

	clientId := "tinygo-client-" + randomString(10)
	fmt.Printf("ClientId: %s\n", clientId)

	// Get a transport for MQTT packets
	fmt.Printf("Connecting to MQTT broker at %s\n", broker)
	conn, err := net.Dial("tcp", broker)
	if err != nil {
		log.Fatal(err)
	}
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
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	err = client.Connect(ctx, conn, &varconn)
	if err != nil {
		log.Fatal("failed to connect: ", err)
	}

	// Subscribe to topic
	ctx, _ = context.WithTimeout(context.Background(), 10*time.Second)
	err = client.Subscribe(ctx, mqtt.VariablesSubscribe{
		PacketIdentifier: 23,
		TopicFilters: []mqtt.SubscribeRequest{
			{TopicFilter: []byte(topic), QoS: mqtt.QoS0},
		},
	})
	if err != nil {
		log.Fatal("failed to subscribe to", topic, err)
	}
	fmt.Printf("Subscribed to topic %s\n", topic)

	// Publish on topic
	pubFlags, _ := mqtt.NewPublishFlags(mqtt.QoS0, false, false)
	pubVar := mqtt.VariablesPublish{
		TopicName: []byte(topic),
	}

	for i := 0; i < 10; i++ {
		if !client.IsConnected() {
			log.Fatal("client disconnected: ", client.Err())
		}

		freq := float32(machine.CPUFrequency()) / 1000000
		payload := fmt.Sprintf("%.02fMhz", freq)

		pubVar.PacketIdentifier++
		err = client.PublishPayload(pubFlags, pubVar, []byte(payload))
		if err != nil {
			log.Fatal("error transmitting message: ", err)
		}

		time.Sleep(time.Second)

		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		err = client.HandleNext()
		if err != nil {
			log.Fatal("handle next: ", err)
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
