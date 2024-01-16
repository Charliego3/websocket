package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
	"time"
)

type FutureResp struct {
	Data []struct {
		Date      int64  `json:"date"`
		Price     string `json:"price"`
		Amount    string `json:"amount"`
		Tid       int64  `json:"tid"`
		Type      string `json:"type"`
		TradeType string `json:"trade_type"`
	} `json:"data"`
	No      int    `json:"no"`
	Channel string `json:"channel"`
}

type TestReceiver struct{}

func (r *TestReceiver) Unmarshal(frameType FrameType, reader io.Reader) (any, error) {
	bytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if bytes[0] != '{' {
		return string(bytes), nil
	}
	resp := new(FutureResp)
	return resp, json.Unmarshal(bytes, resp)
}

func (r *TestReceiver) OnMessage(msg any) {
	fmt.Printf("收到消息: %+v\n", msg)
}

func TestClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClient(ctx, "wss://api.bw6.com/websocket", new(TestReceiver),
		WithHeartbeatInterval(time.Second),
		WithAutoReConnect(),
		WithHeartbeatHandler(func(c *Client) {
			c.SendMessage([]byte("ping"))
		}),
	)
	require.NoError(t, err)

	require.NoError(t, client.SendJson(struct {
		Event   string `json:"event,omitempty"`
		Channel string `json:"channel,omitempty"`
	}{"addChannel", "btcusdt_trades"}))

	//time.AfterFunc(time.Second*10, func() {
	//	cancel()
	//})
	_ = cancel
	select {}
}
