package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

type Message struct {
	*JsonMessage
	Name string
}

func TestConn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClient(
		ctx, "ws://121.40.165.18:8800",
		&LoggedReceiver{}, WithPing(NewStringMessage("ping")),
		// WithPrefix("Tester"),
		WithOnConnected(func(client *Client) {
			client.logger.Info("websocket connected...")
		}),
	)

	err := client.Connect()
	if err != nil {
		log.Fatal(err)
	}

	_ = client.Subscribe(&Message{
		Name: "Joe",
	})

	<-time.Tick(time.Second * 5)
	_ = client.Shutdown()
}
