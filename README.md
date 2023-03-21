# websocket
This is a golang websocket client easy to use

## Usage
```go
import (
	"context"
	"io"

	logger "github.com/charmbracelet/log"
)

type Receiver struct {
	*logger.Logger
}

func (r *Receiver) OnReceive(frame *Frame) {
	bs, err := io.ReadAll(frame.Reader)
	if err != nil {
		r.Error("读取消息失败!", "err", err)
		return
	}
	r.Infof("收到消息: %s", bs)
}

// SetLogger is a Optional func with Processor
func (r *Receiver) SetLogger(l *logger.Logger) {
	r.Logger = l
}

type Message struct {
	*JsonMessage
	Name string
}

func main() {
	ctx := context.Background()
	client := NewClient(ctx, "ws://121.40.165.18:8800", &Receiver{}, WithPing(NewStringMessage("ping")))
	err := client.Connect()
	if err != nil {
		logger.Fatal(err)
	}

	client.Subscribe(&Message{
		Name: "Joe",
	})

	<-make(chan struct{})
}
```