package websocket

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

type Receiver struct {
	logger *log.Logger
}

func (r *Receiver) OnReceive(frame *Frame) {
	bs, err := io.ReadAll(frame.Reader)
	if err != nil {
		r.logger.Error("read message error", "err", err)
		return
	}
	r.logger.Info("receive", "message", string(bs))
}

func (r *Receiver) SetLogger(logger *log.Logger) {
	r.logger = logger
}

type Message struct {
	*JsonMessage
	Name string
}

func TestConn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClient(
		ctx, "ws://121.40.165.18:8800",
		&Receiver{}, WithPing(NewStringMessage("ping")),
		WithPrefix("Tester"),
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
