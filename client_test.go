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
	resp := new(FutureResp)
	decoder := json.NewDecoder(reader)
	err := decoder.Decode(resp)
	return resp, err
}

func (r *TestReceiver) OnMessage(msg any) {
	fmt.Printf("收到消息: %+v\n", msg)
}

func TestClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClient(ctx, "wss://api.bw6.com/websocket", new(TestReceiver))
	require.NoError(t, err)

	require.NoError(t, client.SendJson(struct {
		Event   string `json:"event,omitempty"`
		Channel string `json:"channel,omitempty"`
	}{"addChannel", "btcusdt_trades"}))

	time.AfterFunc(time.Minute*5, func() {
		cancel()
	})
	select {}
}
