// Command smoke is a small MCP client for exercising a running rdp-mcp service.
//
//	go run ./tools/smoke -url http://127.0.0.1:8765/mcp -machine dev-pc [-token T] [-out shot.png] [-run "hostname"]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	token string
	rt    http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

func main() {
	url := flag.String("url", "http://127.0.0.1:8765/mcp", "MCP endpoint")
	token := flag.String("token", "", "MCP bearer token")
	machine := flag.String("machine", "", "machine to test (default: first listed)")
	out := flag.String("out", "smoke.png", "where to save the screenshot")
	run := flag.String("run", "", "optional command for run_command (e.g. hostname)")
	input := flag.Bool("input", false, "also send a mouse move and a harmless key (shift)")
	callTool := flag.String("call", "", "call a single tool and exit, e.g. -call mouse -args '{\"machine\":\"win1\",\"action\":\"move\",\"x\":10,\"y\":10}'")
	callArgs := flag.String("args", "{}", "JSON arguments for -call")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	hc := &http.Client{}
	if *token != "" {
		hc.Transport = bearer{*token, http.DefaultTransport}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "rdp-mcp-smoke", Version: "0.1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: *url, HTTPClient: hc}, nil)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("tools:")
	for _, t := range tools.Tools {
		fmt.Print(" ", t.Name)
	}
	fmt.Println()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		start := time.Now()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		fmt.Printf("\n== %s %v (%s, isError=%v)\n", name, args, time.Since(start).Round(time.Millisecond), res.IsError)
		for _, c := range res.Content {
			switch v := c.(type) {
			case *mcp.TextContent:
				fmt.Println(v.Text)
			case *mcp.ImageContent:
				fmt.Printf("[image %s, %d bytes]\n", v.MIMEType, len(v.Data))
			}
		}
		return res
	}

	if *callTool != "" {
		var args map[string]any
		if err := json.Unmarshal([]byte(*callArgs), &args); err != nil {
			log.Fatalf("-args: %v", err)
		}
		res := call(*callTool, args)
		for _, c := range res.Content {
			if img, ok := c.(*mcp.ImageContent); ok {
				if err := os.WriteFile(*out, img.Data, 0o644); err == nil {
					fmt.Println("saved", *out)
				}
			}
		}
		if res.IsError {
			os.Exit(1)
		}
		return
	}

	call("list_machines", nil)
	if *machine == "" {
		log.Fatal("pass -machine")
	}
	res := call("screenshot", map[string]any{"machine": *machine})
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			if err := os.WriteFile(*out, img.Data, 0o644); err == nil {
				fmt.Println("saved", *out)
			}
		}
	}
	if *input {
		call("mouse", map[string]any{"machine": *machine, "action": "move", "x": 100, "y": 100, "screenshot_after": false})
		call("keyboard", map[string]any{"machine": *machine, "action": "press", "keys": []string{"shift"}, "screenshot_after": false})
		call("wait", map[string]any{"machine": *machine, "until_stable": true, "seconds": 3, "screenshot_after": false})
	}
	if *run != "" {
		call("run_command", map[string]any{"machine": *machine, "command": *run, "timeout": 60})
	}
	call("session", map[string]any{"machine": *machine, "action": "status"})
}
