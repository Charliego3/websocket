package websocket

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
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

func TestMockServer(t *testing.T) {
	http.HandleFunc("/ws", serveWS)
	time.AfterFunc(time.Second, func() {
		fmt.Println("ws served")
	})
	http.ListenAndServe(":9999", nil)
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}

	defer conn.Close()
	for {
		mt, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("read:", err)
			break
		}
		fmt.Printf("recv: %s\n", message)
		err = conn.WriteMessage(mt, message)
		if err != nil {
			fmt.Println("write:", err)
			break
		}
	}
}

type ReconnectReceiver struct {
	ReaderReceiver
}

func (ReconnectReceiver) OnMessage(v any) {
	b, err := io.ReadAll(v.(io.Reader))
	if err != nil {
		fmt.Println("receive:", err)
		return
	}

	fmt.Println("rece:", string(b))
}

func TestReconnect(t *testing.T) {
	for range 50 {
		go func() {
			NewClient(
				context.Background(),
				"wss://hk001web.huosx.com/imws?chatId=1002130795&type=c_notify_message&token=37fdeab26cc94231bceb850aa45bbb63&systemType=3&terminals=2",
				new(ReconnectReceiver),
				WithAutoReConnect(),
				WithHeartbeatInterval(time.Second),
				WithHeartbeatHandler(func(c *Client) {
					c.SendMessage([]byte("PING"))
				}),
				WithConnected(func(c *Client) {
					c.SendMessage([]byte("first connected"))
				}),
				WithReconnected(func(c *Client) {
					c.SendMessage([]byte("after reconnected"))
				}),
			)
			select {}
		}()
	}
	select {}
}

func TestAc(t *testing.T) {
	chatId := ""
	ch := make(chan struct{})
	for i, u := range read() {
		go func(i int, user []string) {
			tChatId := chatId
			if chatId == "" {
				tChatId = user[0]
			}
			url := fmt.Sprintf("wss://hk001web.huosx.com/imws?chatId=%s&type=c_notify_message&token=%s&systemType=3&terminals=2", tChatId, user[1])
			fmt.Println(url)
			run(i, user[0], url, tChatId, ch)
		}(i, u)
	}
	select {}
}

func read() [][]string {
	file, err := os.Open("userdata.csv")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	// 读取 CSV 内容
	reader := csv.NewReader(file)

	// 逐行读取
	records, err := reader.ReadAll()
	if err != nil {
		panic(err)
	}

	// 遍历 CSV 记录
	var data [][]string
	for i, record := range records {
		if i == 0 {
			// 跳过表头
			continue
		}
		if len(record) < 2 {
			// 保护性判断，防止越界
			continue
		}
		data = append(data, record)
	}
	return data
}

type AcReceiver struct {
	userId string
	chatId string
}

func (r *AcReceiver) Unmarshal(frameType FrameType, reader io.Reader) (any, error) {
	bytes, err := io.ReadAll(reader)
	return string(bytes), err
}

func (r *AcReceiver) OnMessage(msg any) {
	fmt.Printf("收到消息：%s -> %s -> %s\n", r.userId, r.chatId, msg)
}

func run(i int, userId, url, chatId string, ch chan struct{}) {
	client, err := NewClient(
		context.Background(),
		url,
		&AcReceiver{userId: userId, chatId: chatId},
		WithReadTimeout(time.Minute*30),
		WithConnectTimeout(time.Minute*3),
		WithLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})).With("Index", i)),
		WithErrorHandler(func(ft FrameType, e Event, err error) {
			fmt.Printf("error: %s -> %v\n", userId, err)
		}),
		WithHeartbeatHandler(func(client *Client) {
			client.SendMessage([]byte("PING"))
		}),
		WithHeartbeatInterval(time.Second*10),
	)
	if err != nil {
		panic(err)
	}

	// client.SendMessage(fmt.Appendf([]byte{}, "{cmd: \"add\", channels: [{chatId: \"%s\", messageType: \"notify_message\"}]}", chatId))
	// client.SendMessage(fmt.Appendf([]byte{}, "{\"cmd\":\"add\",\"channels\":[{\"chatId\":\"%s\",\"messageType\":\"live_crow_message\"}]}", chatId))
	<-ch
	_ = client.Shutdown()
}
