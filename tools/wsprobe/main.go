// Command wsprobe exercises the live view WebSocket (GET /api/v1/machines/{name}/stream).
//
//	go run ./tools/wsprobe -url http://127.0.0.1:8765 -token ADMIN -machine win1 [-seconds 5] [-move]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/coder/websocket"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:8765", "service base URL")
	token := flag.String("token", "", "admin token")
	machine := flag.String("machine", "", "machine name")
	seconds := flag.Int("seconds", 5, "how long to listen")
	move := flag.Bool("move", false, "send a mouse move (only applied in human mode)")
	flag.Parse()
	u := strings.Replace(*base, "http", "ws", 1) + "/api/v1/machines/" + *machine + "/stream?fps=10&token=" + *token
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*seconds)*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer c.CloseNow()
	if *move {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"mouse","action":"move","x":50,"y":60}`))
	}
	frames, bytes := 0, 0
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageBinary {
			frames++
			bytes += len(data)
			if frames == 1 {
				fmt.Printf("first frame: %d bytes, jpeg=%v\n", len(data), len(data) > 2 && data[0] == 0xFF && data[1] == 0xD8)
			}
			continue
		}
		s := string(data)
		if len(s) > 160 {
			s = s[:160] + "..."
		}
		fmt.Println("text:", s)
	}
	fmt.Printf("frames=%d bytes=%d\n", frames, bytes)
}
