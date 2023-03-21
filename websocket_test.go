package websocket

import (
	"context"
	"io"
	"testing"

	"github.com/charmbracelet/log"
)

type Receiver struct{}

func (r *Receiver) OnReceive(frame *Frame) {
	bs, err := io.ReadAll(frame.Reader)
	if err != nil {
		log.Error("读取消息失败!", "err", err)
		return
	}
	log.Infof("收到消息: %s", bs)
}

type Message struct {
	*JsonMessage
	Name string
}

func TestConn(t *testing.T) {
	ctx := context.Background()
	client := NewClient(ctx, "ws://121.40.165.18:8800", &Receiver{}, WithPing(NewStringMessage("ping")))
	err := client.Connect()
	if err != nil {
		log.Fatal(err)
	}

	client.Subscribe(&Message{
		Name: "Joe",
	})

	<-make(chan struct{})
}
